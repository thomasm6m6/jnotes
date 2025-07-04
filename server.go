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
	"strconv"
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
	Content         string
	Preview         string
	AttachmentCount int
}

type FileResponse struct {
	FileName        string `json:"fileName"`
	Content         string `json:"content"`
	AttachmentCount int    `json:"attachmentCount"`
}

type FileInfo struct {
	FileName        string `json:"fileName"`
	Preview         string `json:"preview"`
	AttachmentCount int    `json:"attachmentCount"`
}

type IndexResponse struct {
	Files []FileInfo `json:"files"`
}

type SaveRequest struct {
	FileName string `json:"fileName"`
	Text     string `json:"text"`
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

		attachmentCount := 0
		filesDir := filepath.Join(DB_PATH, dir.Name(), "files")
		if f, err := os.Stat(filesDir); err == nil && f.IsDir() {
			if entries, err := os.ReadDir(filesDir); err == nil {
				attachmentCount = len(entries)
			} else {
				log.Printf("could not read files dir for counting %s: %v", filesDir, err)
			}
		}

		basename := dir.Name()
		newCache[basename] = CachedFile{
			Content:         string(content),
			Preview:         preview,
			AttachmentCount: attachmentCount,
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
				FileName:        name,
				Preview:         cachedFile.Preview,
				AttachmentCount: cachedFile.AttachmentCount,
			})
		}

		today := time.Now().Format("20060102")
		if _, exists := filemap[today]; !exists {
			fileInfos = append(fileInfos, FileInfo{FileName: today, Preview: "", AttachmentCount: 0})
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
						FileName:        name,
						Preview:         cachedFile.Preview,
						AttachmentCount: cachedFile.AttachmentCount,
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
	var attachmentCount int
	if exists {
		content = cachedFile.Content
		attachmentCount = cachedFile.AttachmentCount
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(FileResponse{
		FileName:        name,
		Content:         content,
		AttachmentCount: attachmentCount,
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

	dateStr := req.FileName
	if !regexp.MustCompile(`^\d{8}$`).MatchString(dateStr) {
		httpError(w, http.StatusBadRequest, errors.New("invalid or missing fileName in save request"))
		return
	}
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
	cached, exists := fileCache[basename]
	if !exists {
		cached = CachedFile{}
	}
	cached.Content = req.Text
	cached.Preview = preview
	fileCache[basename] = cached
	cacheMu.Unlock()

	debounceSync()

	_ = json.NewEncoder(w).Encode(SaveResponse{Status: "save scheduled"})
}

func handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	noteName := r.URL.Query().Get("note")
	index := r.URL.Query().Get("index")
	if noteName == "" || index == "" {
		httpError(w, http.StatusBadRequest, errors.New("missing note or index"))
		return
	}

	if !regexp.MustCompile(`^\d{8}$`).MatchString(noteName) {
		httpError(w, http.StatusBadRequest, errors.New("invalid note name"))
		return
	}

	filesDir := filepath.Join(DB_PATH, noteName, "files")
	entries, err := os.ReadDir(filesDir)
	if err != nil {
		httpError(w, http.StatusNotFound, fmt.Errorf("attachments not found: %w", err))
		return
	}

	sort.Slice(entries, func(i, j int) bool {
		iName := strings.TrimSuffix(entries[i].Name(), filepath.Ext(entries[i].Name()))
		jName := strings.TrimSuffix(entries[j].Name(), filepath.Ext(entries[j].Name()))
		iVal, errI := strconv.Atoi(iName)
		jVal, errJ := strconv.Atoi(jName)
		if errI != nil || errJ != nil {
			return entries[i].Name() < entries[j].Name()
		}
		return iVal < jVal
	})

	indexInt, err := strconv.Atoi(index)
	if err != nil || indexInt < 0 || indexInt >= len(entries) {
		httpError(w, http.StatusBadRequest, errors.New("invalid index"))
		return
	}

	fileName := entries[indexInt].Name()
	redirectURL := fmt.Sprintf("/db/%s/files/%s", noteName, fileName)
	http.Redirect(w, r, redirectURL, http.StatusFound)
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
	http.HandleFunc("/getattachment", handleGetAttachment)

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/", fs)

	dbfs := http.FileServer(http.Dir(DB_PATH))
	http.Handle("/db/", http.StripPrefix("/db/", dbfs))

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
