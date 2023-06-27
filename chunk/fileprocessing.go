package chunk

import (
	"bytes"
	"log"
	"errors"
	"fmt"
	"io/ioutil"
	"mime/multipart"
	"strings"

	"github.com/neurosnap/sentences/english"
	tke "github.com/pkoukk/tiktoken-go"
	"golang.org/x/text/transform"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"github.com/gabriel-vasile/mimetype"


	"github.com/saintfish/chardet"


	"io"
	"code.sajari.com/docconv"
	"github.com/gen2brain/go-fitz"

	"archive/zip"
)

type Chunk struct {
	Start int
	End   int
	Title string
	Text  string
}

// MaxTokensPerChunk is the maximum number of tokens allowed in a single chunk for OpenAI embeddings
// MaxTokensPerChunk is the maximum number of tokens allowed in a single chunk for OpenAI embeddings
const MaxTokensPerChunk = 1500
const EmbeddingModel = "text-embedding-ada-002"

// ExtractFilesFromZip extracts the text from files within a zip file
func ExtractFilesFromZip(f multipart.File) ([]string, []string, error) {
	// Convert the multipart.File to a []byte
	fileBytes, err := ioutil.ReadAll(f)
	if err != nil {
			return nil, nil, err
	}

	// Create a reader for the zip file
	zipReader, err := zip.NewReader(bytes.NewReader(fileBytes), int64(len(fileBytes)))
	if err != nil {
			return nil, nil, err
	}

			
	// Create a reader for the zip file
	zipReader, err := zip.NewReader(f, f.Size())
	if err != nil {
		return nil, nil, err
	}

	// Initialize a slice to store the content of each file
	var fileContents []string
	var fileNames []string

	// Iterate over the files in the zip archive
	for _, file := range zipReader.File {
		// Skip directories
		if file.FileInfo().IsDir() {
			continue
		}

		// Open the current file
		currentFile, err := file.Open()
		if err != nil {
			return nil, nil, err
		}
		defer currentFile.Close()

		// Read the file's content
		contentBytes, err := ioutil.ReadAll(currentFile)
		if err != nil {
			return nil, nil, err
		}

		// Convert the file's content to a string and append it to the slice
		fileContents = append(fileContents, string(contentBytes))

		// Append the file name to the fileNames slice
		fileNames = append(fileNames, file.Name)
	}

	// Return the slice with file contents and file names
	return fileContents, fileNames, nil
}

func CreateChunks(fileContent string, title string) ([]Chunk, error) {
	chunks := []Chunk{}

	// Initialize sentence tokenizer
	tokenizer, _ := english.NewSentenceTokenizer(nil)
	sentences := tokenizer.Tokenize(fileContent)

	// Get tiktoken encoding for the model
	tiktoken, err := tke.EncodingForModel(EmbeddingModel)
	if err != nil {
		return []Chunk{}, fmt.Errorf("getEncoding: %v", err)
	}

	chunkStart := 0

	for chunkStart < len(sentences) {
		tokenCount := 0
		chunkText := ""
		chunkSentences := 0

		for i := chunkStart; i < len(sentences) && tokenCount < MaxTokensPerChunk; i++ {
			sentence := sentences[i].Text
			tiktokens := tiktoken.Encode(sentence, nil, nil)
			sentenceTokenCount := len(tiktokens)

			if sentenceTokenCount > MaxTokensPerChunk {
				continue // Skip sentence if longer than MaxTokensPerChunk
			}

			if tokenCount+sentenceTokenCount <= MaxTokensPerChunk {
				tokenCount += sentenceTokenCount
				chunkText += " " + sentence
				chunkSentences++
			} else {
				break
			}
		}

		trimmedText := strings.TrimSpace(chunkText)
		if len(trimmedText) > 0 {
			chunks = append(chunks, Chunk{
				Start: chunkStart,
				End:   chunkStart + tokenCount,
				Title: title,
				Text:  trimmedText,
			})
		}

		// Calculate stride dynamically based on chunk sentences
		sentenceStride := chunkSentences / 5
		if sentenceStride == 0 {
			sentenceStride = 1
		}

		// Move chunkStart forward by sentenceStride
		chunkStart += sentenceStride
	}

	if len(chunks) == 0 {
		return nil, errors.New("no chunks created")
	}

	return chunks, nil
}

// Add a new function to handle zip files
func processZipFile(content []byte) ([]string, error) {
	zipReader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, err
	}

	var fileContents []string

	for _, file := range zipReader.File {
		if file.FileInfo().IsDir() {
			continue
		}

		currentFile, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer currentFile.Close()

		fileContent, err := ioutil.ReadAll(currentFile)
		if err != nil {
			return nil, err
		}

		// Assume that the content is a UTF-8 encoded string
		fileContents = append(fileContents, string(fileContent))
	}

	return fileContents, nil
}

func GetTextFromFile(f multipart.File) (string, error) {
	content, err := ioutil.ReadAll(f)
	if err != nil {
		return "", err
	}

	mime := mimetype.Detect(content)
	contentType := mime.String()

	var text string
	var texts []string

	log.Println("[GetTextfromFile] ContentType:", contentType)
	switch contentType {
	case "application/zip": // .zip
		log.Println("[GetTextfromFile] .zip file encountered...")
		texts, err = processZipFile(content)
		if err != nil {
			return "", fmt.Errorf("error converting .zip file: %v", err)
		}
		// Join all file contents with a space to make a single string
		text = strings.Join(texts, " ")

	case "application/msword": // .doc
		log.Println("[GetTextfromFile] .doc file encountered...")
		text, _, err = docconv.ConvertDoc(bytes.NewReader(content))
		if err != nil {
			return "", fmt.Errorf("error converting .doc file")
		}
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document": // .docx
		log.Println("[GetTextfromFile] .docx file encountered...")
		text, _, err = docconv.ConvertDocx(bytes.NewReader(content))
		if err != nil {
			return "", fmt.Errorf("error converting .docx file: %v", err)
		}
	case "application/epub+zip": // .epub
		log.Println("[GetTextfromFile] .epub file encountered...")
		fitzDoc, err := fitz.NewFromReader(bytes.NewReader(content))
		if err != nil {
			return "", fmt.Errorf("error reading .epub file: %v", err)
		}
		defer fitzDoc.Close()
		for i := 0; i < fitzDoc.NumPage(); i++ {
			pageText, err := fitzDoc.Text(i)
			if err != nil {
				return "", fmt.Errorf("error getting text from page %d: %v", i, err)
			}
			// Preprocess the text by replacing newline characters with spaces
			pageText = strings.ReplaceAll(pageText, "\n", " ")
			text += pageText
		}
	default: // Assume plain text
		detector := chardet.NewTextDetector()
		result, err := detector.DetectBest(content)
		if err != nil {
			return "", fmt.Errorf("error detecting encoding: %v", err)
		}

		if strings.ToLower(result.Charset) == "utf-8" {
			text = string(content)
		} else {
			var enc encoding.Encoding
			switch strings.ToLower(result.Charset) {
			case "iso-8859-1":
				enc = charmap.ISO8859_1
			case "windows-1252":
				enc = charmap.Windows1252
			// Add more encodings here as needed
			default:
				return "", fmt.Errorf("unsupported encoding: %s", result.Charset)
			}

			text, _, err = transform.String(enc.NewDecoder(), string(content))
			if err != nil {
				return "", fmt.Errorf("error decoding content: %v", err)
			}
		}
	}

	return text, nil
}

// extract human-readable text from a given pdf with support for spaces/whitespace.
func ExtractTextFromPDF(f multipart.File, fileSize int64) (string, error) {
	// Reset the file reader's position
	_, err := f.Seek(0, io.SeekStart)
	if err != nil {
		return "", err
	}

	// Convert the uploaded file to a human-readable text
	bodyResult, _, err := docconv.ConvertPDF(f)
	if err != nil {
		return "", err
	}

	// Remove extra whitespace and newlines
	text := strings.TrimSpace(bodyResult)

	return text, nil
}