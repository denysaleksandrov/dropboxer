package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	li "logwrapper"

	"github.com/sirupsen/logrus"
)

const (
	URL        = "https://api.dropboxapi.com/2/files"
	UploadURL  = "https://content.dropboxapi.com/2/files"
	BEARER     = "some key here"
	BASEFOLDER = "some base folder here"
)

type Folders struct {
	Entries []Folder `json:"entries"`
	Cursor  string   `json:"cursor"`
	More    bool     `json:"has_more"`
}

type Folder struct {
	Tag         string `json:".tag"`
	Name        string `json:"name"`
	PathLower   string `json:"path_lower"`
	PathDisplay string `json:"path_display"`
	ID          string `json:"id"`
}

type File struct {
	Name           string `json:"name"`
	PahtLower      string `json:"paht_lower"`
	PathDisplay    string `json:"path_display"`
	ID             string `json:"id"`
	ClientModified string `json:"client_modified"`
	ServerModified string `json:"server_modified"`
	Rev            string `json:"rev"`
	Size           int    `json:"size"`
	ContentHash    string `json:"content_hash"`
}

type Matches struct {
	Matches []Match `json:"matches"`
	More    bool    `json:"more"`
	Start   int     `json:"start"`
}

type Match struct {
	Metadata struct {
		PathDisplay string `json:"path_display"`
	} `json:"metadata"`
}

type SearchData struct {
	Path  string `json:"path"`
	Query string `json:"query"`
	Mode  Mode   `json:"mode"`
}

type Mode struct {
	Tag string `json:".tag"`
}

var client http.Client
var flFormatter string
var flFile string
var flFolder string
var flRFolder string
var flList bool
var flUpload bool
var flCreate bool
var logger *li.StandardLogger

func init() {
	flag.StringVar(&flFormatter, "fmt", "json", "pick logger formatter: json(default) or text")
	flag.StringVar(&flFile, "file", "", "name of file to be uploaded")
	flag.BoolVar(&flList, "list", false, "if set will list content of specified or root(default) folder")
	flag.BoolVar(&flUpload, "upload", false, "if set will upload specified file or folder")
	flag.BoolVar(&flCreate, "create", false, "if set will create a folder")
	flag.StringVar(&flFolder, "folder", "", "name of folder to be uploaded, recursivelly")
	flag.StringVar(&flRFolder, "rfolder", "", "name of remote folder")

	logger = li.NewLogger()
}

func list(path string) (error, bool) {
	if !strings.HasPrefix(path, "/") && len(path) > 1 {
		path = fmt.Sprintf("/%s", path)
	}
	body, err := json.Marshal(map[string]string{
		"path": path,
	})
	if err != nil {
		return err, false
	}
	req, err := http.NewRequest("POST", fmt.Sprintf("%s%s", URL, "/list_folder"), bytes.NewBuffer(body))
	req.Header.Set("Content-type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", BEARER))
	if err != nil {
		return err, false
	}
	resp, err := client.Do(req)
	if err != nil {
		return err, false
	}
	defer resp.Body.Close()

	var result Folders
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err, false
	}
	for _, entry := range result.Entries {
		fmt.Println(entry.Name)
	}
	return nil, true
}

func search(path, file string) bool {
	if path != "" {
		path = fmt.Sprintf("/%s", path)
	}
	body := []byte(fmt.Sprintf(`{"path": "%s", "query": "%s", "mode": {".tag":"filename"}}`, path, file))
	//mode := Mode{Tag: "filename"}
	//data := SearchData{
	//	Path:  fmt.Sprintf("%s", path),
	//	Query: file,
	//	Mode:  mode,
	//}
	//body, err := json.Marshal(data)
	//if err != nil {
	//	return false
	//}
	req, err := http.NewRequest("POST", fmt.Sprintf("%s%s", URL, "/search"), bytes.NewBuffer(body))
	req.Header.Set("Content-type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", BEARER))
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := ioutil.ReadAll(resp.Body)
		logger.Warnf("Search failed. Result code is %d due to: %s", resp.StatusCode, string(b))
		return false
	}

	var result Matches
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false
	}
	if path == "" {
		path = file
	} else {
		path = fmt.Sprintf("%s/%s", path, file)
	}
	for _, match := range result.Matches {
		if match.Metadata.PathDisplay == path {
			return true
		}
	}
	return false
}

func upload_file(fileName, localPath, remotePath string) (error, bool) {
	file, err := os.Open(fmt.Sprintf("%s/%s", localPath, fileName))
	if err != nil {
		return err, false
	}

	if exists := search(remotePath, fileName); exists {
		logger.Infof("File %s/%s/%s exists. Skipping", BASEFOLDER, remotePath, fileName)
		return nil, true
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s%s", UploadURL, "/upload"), file)
	if err != nil {
		return err, false
	}
	req.Header.Set("Content-type", "application/octet-stream")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", BEARER))
	if remotePath == "" {
		req.Header.Set("Dropbox-API-Arg", fmt.Sprintf("{\"path\": \"/%s\"}", fileName))
	} else {
		req.Header.Set("Dropbox-API-Arg", fmt.Sprintf("{\"path\": \"/%s/%s\"}", remotePath, fileName))
	}
	resp, err := client.Do(req)
	if err != nil {
		return err, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("Result code is %d due to: %s", resp.StatusCode, string(b)), false
	}

	var result File
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err, false
	}
	logger.Info(fmt.Sprintf("File %s uploaded to \"%s\". Size is %d", fileName, result.PathDisplay, result.Size))
	return nil, true
}

func upload_folder(localFolder, remoteFolder string) (error, bool) {
	files, err := ioutil.ReadDir(localFolder)
	if err != nil {
		return err, false
	}
	ch := make(chan string)
	numberOfFiles := len(files)
	for _, file := range files {
		if file.IsDir() {
			numberOfFiles = numberOfFiles - 1
			if err, ok := createRemoteFolder(file.Name(), remoteFolder); ok {
				upload_folder(fmt.Sprintf("%s/%s", localFolder, file.Name()), fmt.Sprintf("%s/%s", remoteFolder, file.Name()))
			} else {
				logger.Infof("Couldn't create a remote folder %s/%s", remoteFolder, file.Name())
				logger.Warn(err)
			}
		} else {
			go func(file os.FileInfo, localFolder, remoteFolder string, ch chan<- string) {
				start := time.Now()
				if err, ok := upload_file(file.Name(), localFolder, remoteFolder); !ok {
					logger.Infof("Coldn't upload file %s to remote folder %s.", file.Name(), remoteFolder)
					logger.Warn(err)
				}
				secs := time.Since(start).Seconds()
				ch <- fmt.Sprintf("%.2fs uploading %s finished", secs, file.Name())
			}(file, localFolder, remoteFolder, ch)
		}
	}
	for i := 0; i < numberOfFiles; i++ {
		//logger.Infof("%.2d - %s\n", i, <-ch)
		<-ch
	}

	return nil, true
}

func createRemoteFolder(localFolder, remoteFolder string) (error, bool) {
	body := []byte(fmt.Sprintf(`{"path": "/%s/%s"}`, remoteFolder, localFolder))
	req, err := http.NewRequest("POST", fmt.Sprintf("%s%s", URL, "/create_folder_v2"), bytes.NewBuffer(body))
	req.Header.Set("Content-type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", BEARER))
	if err != nil {
		return err, false
	}
	resp, err := client.Do(req)
	if err != nil {
		return err, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 409 {
		b, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("Result code is %d due to: %s", resp.StatusCode, string(b)), false
	}

	return nil, true
}

func prettyPrintJSON(b []byte) ([]byte, error) {
	var out bytes.Buffer
	err := json.Indent(&out, b, "", "    ")
	return out.Bytes(), err
}

func main() {
	start := time.Now()

	flag.Parse()
	if flFormatter == "text" {
		logger.SetFormatter(&logrus.TextFormatter{
			FullTimestamp: true,
		})
	}
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client = http.Client{Transport: tr, Timeout: time.Second * 10}
	if flList {
		if err, ok := list(flRFolder); !ok {
			logger.Info("Coldn't list folders.")
			logger.Warn(err)
		}
		logger.Info(fmt.Sprintf("%.2fs  elapsed", time.Since(start).Seconds()))
		os.Exit(0)
	}
	if flUpload {
		if flFile != "" {
			if err, ok := upload_file(flFile, flFolder, flRFolder); !ok {
				logger.Infof("Coldn't upload file %s.", flFile)
				logger.Warn(err)
			}
		} else if flFolder != "" {
			if err, ok := upload_folder(flFolder, flRFolder); !ok {
				logger.Infof("Coldn't upload files from folder %s to a remote folder %s", flFolder, flRFolder)
				logger.Warn(err)
			}
		} else {
			logger.Info("Nothing to upload. Exiting")
		}
		logger.Info(fmt.Sprintf("%.2fs  elapsed", time.Since(start).Seconds()))
		os.Exit(0)
	}
	if flCreate {
		if flFolder != "" {
			if err, ok := createRemoteFolder(flFolder, flRFolder); !ok {
				logger.Infof("Couldn't create a remote folder %s/%s", flRFolder, flFolder)
				logger.Warn(err)
			}
		} else {
			logger.Warn("Nothing to create. Exiting.")
		}
		logger.Info(fmt.Sprintf("%.2fs  elapsed", time.Since(start).Seconds()))
		os.Exit(0)
	}
	logger.Info(fmt.Sprintf("%.2fs  elapsed", time.Since(start).Seconds()))
}
