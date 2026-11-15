package sessionmedia

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Tools makes poster frames and iPhone-friendly copies with ffmpeg, if it is
// installed. Results are cached in Dir, keyed by the file's id, size and
// modification time, so an edited render gets new ones.
type Tools struct {
	Dir string
	// FFmpeg is the ffmpeg binary; empty means look it up on PATH and in the
	// usual Homebrew folders (a GUI-launched app has a short PATH).
	FFmpeg string

	mu   sync.Mutex
	jobs map[string]*Job
}

// Job is a running or finished conversion.
type Job struct {
	State string `json:"state"` // running | ready | failed
	Error string `json:"error,omitempty"`
}

// ErrNoFFmpeg means ffmpeg is not installed.
var ErrNoFFmpeg = errors.New("ffmpeg is not installed on the computer running Eggify")

func (t *Tools) ffmpeg() string {
	if t.FFmpeg != "" {
		return t.FFmpeg
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg", "/usr/bin/ffmpeg"} {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

// Available reports whether ffmpeg was found.
func (t *Tools) Available() bool { return t.ffmpeg() != "" }

func cacheKey(it Item) string {
	return fmt.Sprintf("%s-%d-%d", it.ID, it.Size, it.Modified.Unix())
}

// Poster returns a JPEG frame from about a second into a video.
func (t *Tools) Poster(ctx context.Context, it Item) (string, error) {
	if it.Kind != Video {
		return "", ErrNotMedia
	}
	out := filepath.Join(t.Dir, cacheKey(it)+".jpg")
	if info, err := os.Stat(out); err == nil && info.Size() > 0 {
		return out, nil
	}
	bin := t.ffmpeg()
	if bin == "" {
		return "", ErrNoFFmpeg
	}
	if err := os.MkdirAll(t.Dir, 0o700); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tmp := out + ".tmp.jpg"
	defer os.Remove(tmp)
	// Seek a second in (a fade-in starts black); a shorter clip falls back to its first frame.
	for _, seek := range []string{"1", "0"} {
		cmd := exec.CommandContext(ctx, bin, "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
			"-ss", seek, "-i", it.RealPath, "-frames:v", "1", "-vf", "scale='min(960,iw)':-2", "-q:v", "4", tmp)
		if err := cmd.Run(); err == nil {
			if info, err := os.Stat(tmp); err == nil && info.Size() > 0 {
				return out, os.Rename(tmp, out)
			}
		}
	}
	return "", errors.New("could not read a frame from the video")
}

// Compatible returns the path of the iPhone-friendly copy, if it exists.
func (t *Tools) Compatible(it Item) (string, bool) {
	out := t.compatPath(it)
	info, err := os.Stat(out)
	return out, err == nil && info.Size() > 0
}

func (t *Tools) compatPath(it Item) string {
	ext := ".mp4"
	if it.Kind == Audio {
		ext = ".m4a"
	}
	return filepath.Join(t.Dir, cacheKey(it)+ext)
}

// ConvertStatus reports a conversion's state: "" (none), running, ready or failed.
func (t *Tools) ConvertStatus(it Item) Job {
	if _, ok := t.Compatible(it); ok {
		return Job{State: "ready"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if j := t.jobs[cacheKey(it)]; j != nil {
		return *j
	}
	return Job{}
}

// Convert starts making an H.264/AAC MP4 (video) or AAC M4A (audio) copy in
// the background. Calling it again while it runs, or once it is done, does
// nothing.
func (t *Tools) Convert(it Item) (Job, error) {
	if st := t.ConvertStatus(it); st.State == "ready" || st.State == "running" {
		return st, nil
	}
	bin := t.ffmpeg()
	if bin == "" {
		return Job{}, ErrNoFFmpeg
	}
	if err := os.MkdirAll(t.Dir, 0o700); err != nil {
		return Job{}, err
	}
	key := cacheKey(it)
	t.mu.Lock()
	if t.jobs == nil {
		t.jobs = map[string]*Job{}
	}
	job := &Job{State: "running"}
	t.jobs[key] = job
	t.mu.Unlock()

	go func() {
		out := t.compatPath(it)
		tmp := strings.TrimSuffix(out, filepath.Ext(out)) + ".part" + filepath.Ext(out)
		args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", it.RealPath}
		if it.Kind == Video {
			args = append(args, "-map", "0:v:0", "-map", "0:a:0?", "-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
				"-pix_fmt", "yuv420p", "-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2", "-c:a", "aac", "-b:a", "160k", "-movflags", "+faststart", tmp)
		} else {
			args = append(args, "-vn", "-c:a", "aac", "-b:a", "192k", "-movflags", "+faststart", tmp)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		outBytes, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
		if err == nil {
			err = os.Rename(tmp, out)
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		if err != nil {
			os.Remove(tmp)
			msg := strings.TrimSpace(string(outBytes))
			if len(msg) > 300 {
				msg = msg[len(msg)-300:]
			}
			if msg == "" {
				msg = err.Error()
			}
			job.State, job.Error = "failed", msg
			return
		}
		job.State = "ready"
	}()
	return *job, nil
}
