package postapi

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/h0bb5/vault-ai/chunk"
	openai "github.com/sashabaranov/go-openai"
)

type UploadResponse struct {
	Message             string            `json:"message"`
	NumFilesSucceeded   int               `json:"num_files_succeeded"`
	NumFilesFailed      int               `json:"num_files_failed"`
	SuccessfulFileNames []string          `json:"successful_file_names"`
	FailedFileNames     map[string]string `json:"failed_file_names"`
}

const MAX_FILE_SIZE int64 = 10 << 20         // 10 MB
const MAX_TOTAL_UPLOAD_SIZE int64 = 10 << 20 // 10 MB

func (ctx *HandlerContext) UploadHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MAX_TOTAL_UPLOAD_SIZE)

	err := r.ParseMultipartForm(MAX_TOTAL_UPLOAD_SIZE)
	if err != nil {
		log.Println("[UploadHandler ERR] Error parsing multipart form:", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	files := r.MultipartForm.File["files"]
	uuid := r.FormValue("uuid")
	userProvidedOpenApiKey := r.FormValue("apikey")

	clientToUse := ctx.openAIClient
	if userProvidedOpenApiKey != "" {
		clientToUse = openai.NewClient(userProvidedOpenApiKey)
	}

	responseData := UploadResponse{
		SuccessfulFileNames: make([]string, 0),
		FailedFileNames:     make(map[string]string),
	}

	for _, file := range files {
		fileName := file.Filename
		if file.Size > MAX_FILE_SIZE {
			errMsg := fmt.Sprintf("File size exceeds the %d bytes limit", MAX_FILE_SIZE)
			responseData.NumFilesFailed++
			responseData.FailedFileNames[fileName] = errMsg
			continue
		}

		f, err := file.Open()
		if err != nil {
			errMsg := "Error opening file"
			responseData.NumFilesFailed++
			responseData.FailedFileNames[fileName] = errMsg
			continue
		}
		defer f.Close()

		fileType := file.Header.Get("Content-Type")
		fileContent := ""

		if fileType == "application/pdf" {
			fileContent, err = chunk.ExtractTextFromPDF(f, file.Size)
			if err != nil {
				errMsg := "Error extracting text from PDF"
				responseData.NumFilesFailed++
				responseData.FailedFileNames[fileName] = errMsg
				continue
			}
		} else if fileType == "text/plain" || fileType == "text/html" || fileType == "text/css" || fileType == "application/javascript" || fileType == "application/json" || fileType == "text/typescript" {
			fileContent, err = chunk.GetTextFromFile(f)
			if err != nil {
				errMsg := "Error reading file"
				responseData.NumFilesFailed++
				responseData.FailedFileNames[fileName] = errMsg
				continue
			}
		} else if fileType == "application/zip" {
			fileContents, fileNames, err := chunk.ExtractFilesFromZip(f)
			if err != nil {
				errMsg := "Error extracting files from zip"
				responseData.NumFilesFailed++
				responseData.FailedFileNames[fileName] = errMsg
				continue
			}

			for i, content := range fileContents {
				fileContent = content
				fileName = fileNames[i]
			
				if len(fileContent) > 32 {
					filePreview = fileContent[:32]
				}

				chunks, err := chunk.CreateChunks(fileContent, fileName)
				if err != nil {
					errMsg := "Error chunking file"
					responseData.NumFilesFailed++
					responseData.FailedFileNames[fileName] = errMsg
					continue
				}

				embeddings, err := getEmbeddings(clientToUse, chunks, 100, openai.AdaEmbeddingV2)
				if err != nil {
					errMsg := fmt.Sprintf("Error getting embeddings: %v", err)
					responseData.NumFilesFailed++
					responseData.FailedFileNames[fileName] = errMsg
					continue
				}

				err = ctx.vectorDB.UpsertEmbeddings(embeddings, chunks, uuid)
				if err != nil {
					errMsg := fmt.Sprintf("Error upserting embeddings to vector DB: %v", err)
					responseData.NumFilesFailed++
					responseData.FailedFileNames[fileName] = errMsg
					continue
				}

				responseData.NumFilesSucceeded++
				responseData.SuccessfulFileNames = append(responseData.SuccessfulFileNames, fileName)
			}

			continue
		} else {
			errMsg := "File type not supported"
			responseData.NumFilesFailed++
			responseData.FailedFileNames[fileName] = errMsg
			continue
		}

		if len(fileContent) > 32 {
			filePreview = fileContent[:32]
		}

		chunks, err := chunk.CreateChunks(fileContent, fileName)
		if err != nil {
			errMsg := "Error chunking file"
			responseData.NumFilesFailed++
			responseData.FailedFileNames[fileName] = errMsg
			continue
		}

		embeddings, err := getEmbeddings(clientToUse, chunks, 100, openai.AdaEmbeddingV2)
		if err != nil {
			errMsg := fmt.Sprintf("Error getting embeddings: %v", err)
			responseData.NumFilesFailed++
			responseData.FailedFileNames[fileName] = errMsg
			continue
		}

		err = ctx.vectorDB.UpsertEmbeddings(embeddings, chunks, uuid)
		if err != nil {
			errMsg := fmt.Sprintf("Error upserting embeddings to vector DB: %v", err)
			responseData.NumFilesFailed++
			responseData.FailedFileNames[fileName] = errMsg
			continue
		}

		responseData.NumFilesSucceeded++
		responseData.SuccessfulFileNames = append(responseData.SuccessfulFileNames, fileName)
	}

	if responseData.NumFilesFailed > 0 {
		responseData.Message = "Some files failed to upload and process"
	} else {
		responseData.Message = "All files uploaded and processed successfully"
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	jsonResponse, err := json.Marshal(responseData)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(jsonResponse)
}
