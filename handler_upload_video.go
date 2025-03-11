package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const maxMemory = 1 << 30 //max upload size 1GB
	r.Body = http.MaxBytesReader(w, r.Body, maxMemory)

	// Parse the video ID from the URL
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
	// Check if the user is authorized to upload a video
	video, err := cfg.db.GetVideo(videoID)
    if err != nil {
        respondWithError(w, http.StatusNotFound, "Video not found", err)
        return
    }

	if video.UserID != userID {
        respondWithError(w, http.StatusUnauthorized, "Not authorized to upload thumbnail for this video", nil)
        return
    }

	fmt.Println("uploading video content for video", videoID, "by user", userID)

	
    err = r.ParseMultipartForm(maxMemory)
    if err != nil {
        respondWithError(w, http.StatusBadRequest, "Error parsing form", err)
        return
    }

	file, header, err := r.FormFile("video")
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

    if mediaType != "video/mp4" {
        respondWithError(w, http.StatusBadRequest, "Unsupported video format. Only MP4 are allowed", nil)
        return
    }
	// Create a temporary file to store the uploaded video
	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
    if err != nil {
        respondWithError(w, http.StatusInternalServerError, "Failed to create temporary file", err)
        return
    }
    defer os.Remove(tempFile.Name()) 
    defer tempFile.Close() 

	// Copy the uploaded video to the temporary file
	_, err = io.Copy(tempFile, file)
    if err != nil {
        respondWithError(w, http.StatusInternalServerError, "Failed to save uploaded file", err)
        return
    }
	// Reset the file pointer to the beginning of the file
	_, err = tempFile.Seek(0, io.SeekStart)
    if err != nil {
        respondWithError(w, http.StatusInternalServerError, "Failed to process file", err)
        return
    }

    aspectRatio, err := getVideoAspectRatio(tempFile.Name())
    if err != nil {
        respondWithError(w, http.StatusInternalServerError, "Failed to determine video aspect ratio", err)
        return
    }

    var prefix string
    switch aspectRatio {
    case "16:9":
        prefix = "landscape/"
    case "9:16":
        prefix = "portrait/"
    default:
        prefix = "other/"
    }

	randomBytes := make([]byte, 32)
    _, err = rand.Read(randomBytes)
    if err != nil {
        respondWithError(w, http.StatusInternalServerError, "Failed to generate random filename", err)
        return
    }
	randomString := base64.RawURLEncoding.EncodeToString(randomBytes)
	s3Key := fmt.Sprintf("%s%s.mp4",prefix, randomString)
	
    _, err = tempFile.Seek(0, io.SeekStart)
    if err != nil {
        respondWithError(w, http.StatusInternalServerError, "Failed to process file", err)
        return
    }
    
	// Upload the video to S3
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer cancel()

	_, err = cfg.s3Client.PutObject(ctx, &s3.PutObjectInput{
        Bucket:      aws.String(cfg.s3Bucket),
        Key:         aws.String(s3Key),
        Body:        tempFile,
        ContentType: aws.String(mediaType),
    })
	if err != nil {
        respondWithError(w, http.StatusInternalServerError, "Failed to upload to S3", err)
        return
    }

	videoURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, s3Key)
    video.VideoURL = &videoURL

	err = cfg.db.UpdateVideo(video)
    if err != nil {
        respondWithError(w, http.StatusInternalServerError, "Failed to update video metadata", err)
        return
    }
	respondWithJSON(w, http.StatusOK, video)

}
