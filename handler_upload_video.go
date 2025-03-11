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
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

const (
	maxUploadSize    = 1 << 30 // 1GB
	randomBytesLength = 32
	uploadTimeout    = 5 * time.Minute
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	videoID, err := uuid.Parse(r.PathValue("videoID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid video ID", err)
		return
	}

	userID, err := cfg.authenticateUser(r)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Authentication failed", err)
		return
	}

	video, err := cfg.validateVideoOwnership(w, videoID, userID)
	if err != nil {
		return // Error already handled in validateVideoOwnership
	}

	fmt.Printf("Uploading video content for video %s by user %s\n", videoID, userID)

	file, mediaType, err := cfg.parseAndValidateFile(w, r)
	if err != nil {
		return // Error already handled
	}
	defer file.Close()

	tempPath, processedPath, err := cfg.processVideoFile(w, file)
	if err != nil {
		return // Error already handled
	}
	defer os.Remove(tempPath)
	defer os.Remove(processedPath)

	aspectRatio, err := getVideoAspectRatio(processedPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to determine video aspect ratio", err)
		return
	}

	s3Key, err := cfg.uploadToS3(w, processedPath, mediaType, aspectRatio)
	if err != nil {
		return // Error already handled
	}

	if err := cfg.updateVideoMetadata(w, video, s3Key); err != nil {
		return // Error already handled
	}

	respondWithJSON(w, http.StatusOK, video)
}

func (cfg *apiConfig) authenticateUser(r *http.Request) (string, error) {
    token, err := auth.GetBearerToken(r.Header)
    if err != nil {
        return "", err
    }
    
    userUUID, err := auth.ValidateJWT(token, cfg.jwtSecret)
    if err != nil {
        return "", err
    }
    
    return userUUID.String(), nil
}

func (cfg *apiConfig) validateVideoOwnership(w http.ResponseWriter, videoID uuid.UUID, userID string) (*database.Video, error) {
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video not found", err)
		return nil, err
	}

	if video.UserID.String() != userID {
		respondWithError(w, http.StatusUnauthorized, "Not authorized to upload video for this content", nil)
		return nil, fmt.Errorf("user %s not authorized for video %s", userID, videoID)
	}

	return &video, nil
}

func (cfg *apiConfig) parseAndValidateFile(w http.ResponseWriter, r *http.Request) (io.ReadCloser, string, error) {
	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Error reading video file", err)
		return nil, "", err
	}

	contentType := header.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type header", err)
		return nil, "", err
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Unsupported video format. Only MP4 is allowed", nil)
		return nil, "", fmt.Errorf("unsupported media type: %s", mediaType)
	}

	return file, mediaType, nil
}

func (cfg *apiConfig) processVideoFile(w http.ResponseWriter, file io.Reader) (string, string, error) {
	tempFile, err := os.CreateTemp("", "tubely-upload-*.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create temporary file", err)
		return "", "", err
	}
	defer tempFile.Close()

	if _, err := io.Copy(tempFile, file); err != nil {
		os.Remove(tempFile.Name())
		respondWithError(w, http.StatusInternalServerError, "Failed to save uploaded file", err)
		return "", "", err
	}

	processedPath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to process video for streaming", err)
		return tempFile.Name(), "", err
	}

	return tempFile.Name(), processedPath, nil
}

func (cfg *apiConfig) uploadToS3(w http.ResponseWriter, filePath, mediaType, aspectRatio string) (string, error) {
	prefix := getAspectRatioPrefix(aspectRatio)

	randomBytes := make([]byte, randomBytesLength)
	if _, err := rand.Read(randomBytes); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to generate random filename", err)
		return "", err
	}

	s3Key := fmt.Sprintf("%s%s.mp4", prefix, base64.RawURLEncoding.EncodeToString(randomBytes))

	file, err := os.Open(filePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to open processed file", err)
		return "", err
	}
	defer file.Close()

	ctx, cancel := context.WithTimeout(context.Background(), uploadTimeout)
	defer cancel()

	_, err = cfg.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(s3Key),
		Body:        file,
		ContentType: aws.String(mediaType),
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to upload to S3", err)
		return "", err
	}

	return s3Key, nil
}

func getAspectRatioPrefix(aspectRatio string) string {
	switch aspectRatio {
	case "16:9":
		return "landscape/"
	case "9:16":
		return "portrait/"
	default:
		return "other/"
	}
}

func (cfg *apiConfig) updateVideoMetadata(w http.ResponseWriter, video *database.Video, s3Key string) error {
	videoURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, s3Key)
	video.VideoURL = &videoURL

	if err := cfg.db.UpdateVideo(*video); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to update video metadata", err)
		return err
	}
	return nil
}