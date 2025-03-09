package main

import (
	"fmt"
	"net/http"
	"io"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"mime"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
    if err != nil {
        respondWithError(w, http.StatusNotFound, "Video not found", err)
        return
    }

	if video.UserID != userID {
        respondWithError(w, http.StatusUnauthorized, "Not authorized to upload thumbnail for this video", nil)
        return
    }

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory = 10 << 20 
    err = r.ParseMultipartForm(maxMemory)
    if err != nil {
        respondWithError(w, http.StatusBadRequest, "Error parsing form", err)
        return
    }

	file, header, err := r.FormFile("thumbnail")
    if err != nil {
        respondWithError(w, http.StatusBadRequest, "Error reading thumbnail file", err)
        return
    }
    defer file.Close()

	contentType := header.Header.Get("Content-Type")
    mediaType, _, err := mime.ParseMediaType(contentType)
    if err != nil {
        respondWithError(w, http.StatusBadRequest, "Invalid Content-Type header", err)
        return
    }

    var extension string
    if mediaType == "image/jpeg" {
        extension = "jpg"
    } else if mediaType == "image/png" {
        extension = "png"
    } else {
        respondWithError(w, http.StatusBadRequest, "Unsupported image format. Only JPEG and PNG are allowed", nil)
        return
    }

	filename := fmt.Sprintf("%s.%s", videoID.String(), extension)
    
    filePath := filepath.Join(cfg.assetsRoot, filename)
    
    outputFile, err := os.Create(filePath)
    if err != nil {
        respondWithError(w, http.StatusInternalServerError, "Failed to create file", err)
        return
    }
    defer outputFile.Close()
    
    _, err = io.Copy(outputFile, file)
    if err != nil {
        respondWithError(w, http.StatusInternalServerError, "Failed to save file", err)
        return
    }
    
    thumbnailURL := fmt.Sprintf("http://localhost:%s/assets/%s", cfg.port, filename)
    video.ThumbnailURL = &thumbnailURL

	err = cfg.db.UpdateVideo(video)
    if err != nil {
        respondWithError(w, http.StatusInternalServerError, "Failed to update video metadata", err)
        return
    }
	respondWithJSON(w, http.StatusOK, video)
}
