package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os/exec"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerVideoMetaCreate(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		database.CreateVideoParams
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

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err = decoder.Decode(&params)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't decode parameters", err)
		return
	}
	params.UserID = userID

	video, err := cfg.db.CreateVideo(params.CreateVideoParams)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create video", err)
		return
	}

	respondWithJSON(w, http.StatusCreated, video)
}

func (cfg *apiConfig) handlerVideoMetaDelete(w http.ResponseWriter, r *http.Request) {
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
		respondWithError(w, http.StatusNotFound, "Couldn't get video", err)
		return
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusForbidden, "You can't delete this video", err)
		return
	}

	err = cfg.db.DeleteVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't delete video", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (cfg *apiConfig) handlerVideoGet(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid video ID", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Couldn't get video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}

func (cfg *apiConfig) handlerVideosRetrieve(w http.ResponseWriter, r *http.Request) {
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

	videos, err := cfg.db.GetVideos(userID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't retrieve videos", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videos)
}


func getVideoAspectRatio(filePath string) (string, error) {
    cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
    
    var stdout bytes.Buffer
    cmd.Stdout = &stdout
    
    if err := cmd.Run(); err != nil {
        return "", fmt.Errorf("error running ffprobe: %w", err)
    }
    
    type ffprobeOutput struct {
        Streams []struct {
            Width  int `json:"width"`
            Height int `json:"height"`
        } `json:"streams"`
    }
    
	var output ffprobeOutput
    if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
        return "", fmt.Errorf("error parsing ffprobe output: %w", err)
    }
    
    var width, height int
    for _, stream := range output.Streams {
        if stream.Width > 0 && stream.Height > 0 {
            width = stream.Width
            height = stream.Height
            break
        }
    }
    
    if width == 0 || height == 0 {
        return "", fmt.Errorf("no valid video stream found with width and height")
    }
    
    ratio := float64(width) / float64(height)
    
    const tolerance = 0.1
    if math.Abs(ratio - 16.0/9.0) < tolerance {
        return "16:9", nil
    } else if math.Abs(ratio - 9.0/16.0) < tolerance {
        return "9:16", nil
    } else {
        return "other", nil
    }
}


func processVideoForFastStart(filePath string) (string, error) {
    outputPath := filePath + ".processing"
    
    cmd := exec.Command(
        "ffmpeg",
        "-i", filePath,       // Input file
        "-c", "copy",         // Copy streams without re-encoding
        "-movflags", "faststart", // Move moov atom to beginning of file
        "-f", "mp4",          // Force mp4 format
        outputPath,           // Output file
    )
    
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    
    if err := cmd.Run(); err != nil {
        return "", fmt.Errorf("error running ffmpeg: %w (stderr: %s)", err, stderr.String())
    }
    
    return outputPath, nil
}