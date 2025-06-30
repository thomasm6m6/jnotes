package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	DB_PATH string
	mu      sync.Mutex
)

type FileResponse struct {
	FileName string `json:"fileName"`
	Content  string `json:"content"`
}

type IndexResponse struct {
	FileNames []string `json:"fileNames"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func init() {
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	DB_PATH = filepath.Join(wd, "db")
}

func httpError(w http.ResponseWriter, status int, err error) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{Error: err.Error()})
}

func handleGetIndex(w http.ResponseWriter, r *http.Request) {
	files, err := os.ReadDir(DB_PATH)
	if err != nil {
		httpError(w, http.StatusInternalServerError, fmt.Errorf("could not fetch file list: %w", err))
		return
	}

	data := IndexResponse{FileNames: []string{}}
	re := regexp.MustCompile(`^\d{8}\.md$`)
	for _, file := range files {
		if re.MatchString(file.Name()) {
			basename, _ := strings.CutSuffix(file.Name(), ".md")
			data.FileNames = append(data.FileNames, basename)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

func handleGetFile(w http.ResponseWriter, r *http.Request) {
	var name string

	q := r.URL.Query()
	if values, ok := q["name"]; ok {
		if len(values) != 1 {
			fmt.Println("here 1")
			httpError(w, http.StatusBadRequest, errors.New("missing or invalid 'name' parameter"))
			return
		} else {
			name = values[0]
			if !regexp.MustCompile(`^\d{8}$`).MatchString(name) {
				fmt.Println("here 2")
				httpError(w, http.StatusBadRequest, errors.New("invalid filename"))
				return
			}
		}
	} else {
		name = time.Now().Format("20060102")
	}

	data, err := os.ReadFile(filepath.Join(DB_PATH, name+".md"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Println("here 3")
		httpError(w, http.StatusInternalServerError,
			fmt.Errorf("error reading file '%s': %s", name, err))
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(FileResponse{
		FileName: name,
		Content:  string(data),
	})
}

func handleSave(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	fmt.Println("handleSave")
	fmt.Fprintln(w, "OK")
	return

	out, err := git("add -A")
	if err != nil {
		fmt.Fprintf(w, "Error with 'git add'\nout: %s\nerr: %s\n", out, err)
		return
	}

	out, err = git("commit -m automated-update")
	if err != nil {
		fmt.Fprintf(w, "Error with 'git commit'\nout: %s\nerr: %s\n", out, err)
		return
	}

	out, err = git("push")
	if err != nil {
		fmt.Fprintf(w, "Error with 'git push'\nout: %s\nerr: %s\n", out, err)
		return
	}

	fmt.Fprintln(w, "saved")
}

func git(cmd string) (string, error) {
	args := strings.Fields(cmd)
	fullArgs := append([]string{"-C", DB_PATH}, args...)
	command := exec.Command("git", fullArgs...)
	out, err := command.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	return trimmed, err
}

func dbExists() bool {
	out, err := git("rev-parse --show-toplevel")
	return err == nil && out == DB_PATH
}

func main() {
	if !dbExists() {
		log.Fatalf("Database (%s) does not exist or is not a git dir", DB_PATH)
		os.Exit(1)
	}

	http.HandleFunc("/getindex", handleGetIndex)
	http.HandleFunc("/getfile", handleGetFile)
	http.HandleFunc("/save", handleSave)

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/", fs)

	log.Println("Serving on port 8080...")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
