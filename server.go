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
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	DB_PATH string
	debounceMu sync.Mutex
	debounceTimer *time.Timer
	debounceDelay = 10 * time.Second
	mu      sync.Mutex
)

type FileResponse struct {
	FileName string `json:"fileName"`
	Content  string `json:"content"`
}

type FileInfo struct {
	FileName string `json:"fileName"`
	Preview  string `json:"preview"`
}

type IndexResponse struct {
	Files []FileInfo `json:"files"`
}

type SaveRequest struct {
	Text string `json:"text"`
}

type SaveResponse struct {
	Status string `json:"status"`
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

func gitPush() (string, error) {
	return git("push")
}

func gitPull() (string, error) {
	if out, err := git("fetch --dry-run"); err != nil {
		return out, err
	} else if len(out) == 0 {
		return "", nil
	}

	return git("pull")
}

func gitSync() {
	if out, err := gitPull(); err != nil {
		log.Printf("'git pull' failed: %s\n%s", err, out)
		return
	}
	if out, err := gitPush(); err != nil {
		log.Printf("'git push' failed: %s\n%s", err, out)
	}
}

func debounceSync() {
	debounceMu.Lock()
	defer debounceMu.Unlock()

	if debounceTimer != nil {
		debounceTimer.Stop()
	}

	debounceTimer = time.AfterFunc(debounceDelay, func() {
		mu.Lock()
		defer mu.Unlock()
		log.Println("running debounced sync...")
		gitSync()
	})
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

	fileInfos := []FileInfo{}
	filemap := make(map[string]bool)
	re := regexp.MustCompile(`^\d{8}\.md$`)

	for _, file := range files {
		if re.MatchString(file.Name()) {
			basename, _ := strings.CutSuffix(file.Name(), ".md")
			filemap[basename] = true

			content, err := os.ReadFile(filepath.Join(DB_PATH, file.Name()))
			if err != nil {
				// We can ignore errors here and just show an empty preview.
				content = []byte{}
			}

			preview := string(content)
			previewRunes := []rune(preview)
			// Truncate to 80 runes for the preview
			if len(previewRunes) > 80 {
				preview = string(previewRunes[0:80])
			}

			fileInfos = append(fileInfos, FileInfo{
				FileName: basename,
				Preview:  preview,
			})
		}
	}

	today := time.Now().Format("20060102")
	if _, exists := filemap[today]; !exists {
		fileInfos = append(fileInfos, FileInfo{FileName: today, Preview: ""})
	}

	sort.Slice(fileInfos, func(i, j int) bool {
		return fileInfos[i].FileName > fileInfos[j].FileName
	})

	data := IndexResponse{Files: fileInfos}
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

	fmt.Println("Calling handleSave...")

	w.Header().Set("Content-Type", "application/json")

	var req SaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("could not decode request: %w", err))
		return
	}

	fileName := time.Now().Format("20060102") + ".md"
	filePath := filepath.Join(DB_PATH, fileName)
	if err := os.WriteFile(filePath, []byte(req.Text), 0644); err != nil {
		httpError(w, http.StatusInternalServerError, fmt.Errorf("could not write file: %w", err))
		return
	}

	if out, err := git("add -A"); err != nil {
		log.Printf("'git add' failed: %s\n%s", err, out)
		httpError(w, http.StatusInternalServerError,
			fmt.Errorf("git add error: %s\n%s", err, out))
		return
	}

	if out, err := git("commit -m automated-update"); err != nil {
		if !strings.Contains(out, "nothing to commit") {
			log.Printf("'git commit' failed: %s\n%s", err, out)
			httpError(w, http.StatusInternalServerError,
				fmt.Errorf("git commit error: %s\n%s", out, err))
			return
		}
	}

	debounceSync()

	_ = json.NewEncoder(w).Encode(SaveResponse{Status: "save scheduled"})
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

	go func() {
		ticker := time.NewTicker(300 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			mu.Lock()
			if out, err := gitPull(); err != nil {
				log.Printf("'git pull' failed: %s\n%s", err, out)
			}
			mu.Unlock()
		}
	}()

	log.Println("Serving on port 8080...")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
