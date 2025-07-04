package main

import (
	"encoding/json"
	"errors"
	"fmt"
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

	"github.com/lithammer/fuzzysearch/fuzzy"
)

var (
	DB_PATH string
	debounceMu sync.Mutex
	debounceTimer *time.Timer
	debounceDelay = 10 * time.Second
	mu      sync.Mutex

	fileCache        = make(map[string]CachedFile)
	cacheMu          sync.RWMutex
	TOTAL_SIZE_LIMIT = int64(10 * 1024 * 1024) // 10MB
)

// CachedFile holds the content and preview of a file for in-memory caching.
type CachedFile struct {
	Content string
	Preview string
}

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

func rebuildCache() error {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	log.Println("Rebuilding file cache...")
	newCache := make(map[string]CachedFile)
	var totalSize int64

	dirs, err := os.ReadDir(DB_PATH)
	if err != nil {
		return fmt.Errorf("could not read db dir for cache rebuild: %w", err)
	}

	re := regexp.MustCompile(`^\d{8}$`)

	// Sort dirs to process newest first, to cache most relevant files
	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i].Name() > dirs[j].Name()
	})

	for _, dir := range dirs {
		if !dir.IsDir() || !re.MatchString(dir.Name()) {
			continue
		}

		notePath := filepath.Join(DB_PATH, dir.Name(), "notes.md")
		info, err := os.Stat(notePath)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Printf("could not get file info for %s: %v", notePath, err)
			}
			continue
		}

		if totalSize+info.Size() > TOTAL_SIZE_LIMIT && totalSize > 0 {
			log.Printf("Cache size limit (%d bytes) reached. Stopping cache population.", TOTAL_SIZE_LIMIT)
			break
		}

		content, err := os.ReadFile(notePath)
		if err != nil {
			log.Printf("could not read file %s for cache: %v", notePath, err)
			continue
		}

		totalSize += info.Size()

		preview := string(content)
		previewRunes := []rune(preview)
		if len(previewRunes) > 80 {
			preview = string(previewRunes[0:80])
		}

		basename := dir.Name()
		newCache[basename] = CachedFile{
			Content: string(content),
			Preview: preview,
		}
	}

	fileCache = newCache
	log.Printf("File cache rebuilt successfully. Cached %d files.", len(fileCache))
	return nil
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

	if err := rebuildCache(); err != nil {
		log.Printf("Failed to rebuild cache after pull: %v", err)
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
	cacheMu.RLock()
	defer cacheMu.RUnlock()

	query := r.URL.Query().Get("q")
	fileInfos := []FileInfo{}

	if query == "" {
		filemap := make(map[string]bool)

		for name, cachedFile := range fileCache {
			filemap[name] = true
			fileInfos = append(fileInfos, FileInfo{
				FileName: name,
				Preview:  cachedFile.Preview,
			})
		}

		today := time.Now().Format("20060102")
		if _, exists := filemap[today]; !exists {
			fileInfos = append(fileInfos, FileInfo{FileName: today, Preview: ""})
		}

		sort.Slice(fileInfos, func(i, j int) bool {
			return fileInfos[i].FileName > fileInfos[j].FileName
		})
	} else {
		type rankedFile struct {
			FileInfo FileInfo
			Rank     int
		}
		var rankedFiles []rankedFile

		for name, cachedFile := range fileCache {
			// RankMatch on empty string is not useful
			if cachedFile.Content == "" {
				continue
			}
			rank := fuzzy.RankMatch(query, cachedFile.Content)
			if rank != -1 {
				rankedFiles = append(rankedFiles, rankedFile{
					FileInfo: FileInfo{
						FileName: name,
						Preview:  cachedFile.Preview,
					},
					Rank: rank,
				})
			}
		}

		sort.Slice(rankedFiles, func(i, j int) bool {
			// Sort by rank, then by filename descending for stable sort
			if rankedFiles[i].Rank != rankedFiles[j].Rank {
				return rankedFiles[i].Rank < rankedFiles[j].Rank
			}
			return rankedFiles[i].FileInfo.FileName > rankedFiles[j].FileInfo.FileName
		})

		for _, rf := range rankedFiles {
			fileInfos = append(fileInfos, rf.FileInfo)
		}
	}

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

	cacheMu.RLock()
	cachedFile, exists := fileCache[name]
	cacheMu.RUnlock()

	var content string
	if exists {
		content = cachedFile.Content
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(FileResponse{
		FileName: name,
		Content:  content,
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

	dateStr := time.Now().Format("20060102")
	dirPath := filepath.Join(DB_PATH, dateStr)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		httpError(w, http.StatusInternalServerError, fmt.Errorf("could not create directory: %w", err))
		return
	}
	filePath := filepath.Join(dirPath, "notes.md")
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

	// Update cache for the saved file.
	basename := dateStr
	preview := req.Text
	previewRunes := []rune(preview)
	if len(previewRunes) > 80 {
		preview = string(previewRunes[0:80])
	}
	cacheMu.Lock()
	fileCache[basename] = CachedFile{
		Content: req.Text,
		Preview: preview,
	}
	cacheMu.Unlock()

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

	if err := rebuildCache(); err != nil {
		log.Fatalf("Failed to build initial file cache: %v", err)
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
			} else {
				if err := rebuildCache(); err != nil {
					log.Printf("Failed to rebuild cache after periodic pull: %v", err)
				}
			}
			mu.Unlock()
		}
	}()

	log.Println("Serving on port 8080...")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
