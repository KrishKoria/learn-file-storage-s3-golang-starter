package main

import (
	"fmt"
	"net/http"
	"io"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
	"encoding/base64"
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

	imageData, err := io.ReadAll(file)
    if err != nil {
        respondWithError(w, http.StatusInternalServerError, "Error reading file data", err)
        return
    }

	encodedData := base64.StdEncoding.EncodeToString(imageData)
    
    mediaType := header.Header.Get("Content-Type")
    dataURL := fmt.Sprintf("data:%s;base64,%s", mediaType, encodedData)
    
    video.ThumbnailURL = &dataURL


	err = cfg.db.UpdateVideo(video)
    if err != nil {
        respondWithError(w, http.StatusInternalServerError, "Failed to update video metadata", err)
        return
    }
	respondWithJSON(w, http.StatusOK, video)
}
