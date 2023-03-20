package main

import (
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

func init() {
	image.RegisterFormat("jpeg", "jpeg", jpeg.Decode, jpeg.DecodeConfig)
}

type WorkingSet struct {
	BufferToPush *[]byte
	BufferToLoad *[]byte
	ImagePath    string
	sync.Mutex
}

func Min(x, y uint32) uint32 {
	if x < y {
		return x
	}
	return y
}

func (ws *WorkingSet) Init(imagePath string) (*image.Rectangle, error) {

	// Try to load file
	ws.ImagePath = imagePath
	imgfile, err := os.Open(ws.ImagePath)
	if err != nil {
		return nil, err
	}
	defer imgfile.Close()

	// Decode image file
	img, _, err := image.Decode(imgfile)
	if err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	size := bounds.Dx() * bounds.Dy() * 3

	// Push image to pushBuffer
	ws.BufferToPush = new([]byte)
	ws.BufferToLoad = new([]byte)
	*ws.BufferToPush = make([]byte, size)
	*ws.BufferToLoad = make([]byte, size)
	for i := 0; i < bounds.Dx(); i++ {
		for j := 0; j < bounds.Dy(); j++ {
			r, g, b, _ := img.At(i, j).RGBA()
			(*ws.BufferToPush)[3*(j*bounds.Dx()+i)] = byte(Min(r/0xff, 255))
			(*ws.BufferToPush)[3*(j*bounds.Dx()+i)+1] = byte(Min(g/0xff, 255))
			(*ws.BufferToPush)[3*(j*bounds.Dx()+i)+2] = byte(Min(b/0xff, 255))
		}
	}

	// Return size of image
	return &bounds, nil
}

func (ws *WorkingSet) Update() error {
	// Open image file
	imgfile, err := os.Open(ws.ImagePath)
	if err != nil {
		return err
	}
	defer imgfile.Close()

	// Decode Image and push to temp buffer
	img, _, err := image.Decode(imgfile)
	if err != nil {
		return err
	}
	bounds := img.Bounds()
	for i := 0; i < bounds.Dx(); i++ {
		for j := 0; j < bounds.Dy(); j++ {
			r, g, b, _ := img.At(i, j).RGBA()
			(*ws.BufferToLoad)[3*(j*bounds.Dx()+i)] = byte(Min(r/0xff, 255))
			(*ws.BufferToLoad)[3*(j*bounds.Dx()+i)+1] = byte(Min(g/0xff, 255))
			(*ws.BufferToLoad)[3*(j*bounds.Dx()+i)+2] = byte(Min(b/0xff, 255))
		}
	}

	// Et on fait tourner les pointeurs
	ws.Lock()
	ws.BufferToPush = ws.BufferToLoad
	ws.Unlock()
	size := bounds.Dx() * bounds.Dy() * 3
	ws.BufferToLoad = new([]byte)
	*ws.BufferToLoad = make([]byte, size)
	return nil
}

func (ws *WorkingSet) PermanentUpdate(fps int) {
	dt := 1 / float64(fps)
	for {
		time.Sleep(time.Duration(int(dt*1000) * 1000000))
		ws.Update()
	}
}

func catchSig(stop *bool, cha chan os.Signal) {
	sig := <-cha
	fmt.Printf("Caught signal %v\n", sig)
	*stop = true
}

func main() {

	IMAGE_FILE := os.Getenv("IMAGE_FILE")
	DASH_FILE := os.Getenv("DASH_FILE")
	STREAM_FPS, err := strconv.Atoi(os.Getenv("STREAM_FPS"))
	if err != nil {
		fmt.Println("env var STREAM_FPS isn't an integer, defaulting to 25")
		STREAM_FPS = 25
	}

	FILE_FPS, err := strconv.Atoi(os.Getenv("STREAM_FPS"))
	if err != nil {
		fmt.Println("env var FILE_FPS isn't an integer, defaulting to 10")
		FILE_FPS = 10
	}

	if IMAGE_FILE == "" {
		fmt.Println("env var IMAGE_FILE isn't set, defaulting to /shared-dir/result.jpg")
		IMAGE_FILE = "/shared-dir/result.jpg"
	}

	if DASH_FILE == "" {
		fmt.Println("env var DASH_FILE isn't set, defaulting to /segment-data/1.mpd")
		DASH_FILE = "../dash-front/public/videos/1.mpd"
	}

	cancelChan := make(chan os.Signal, 1)
	// catch SIGETRM or SIGINTERRUPT
	signal.Notify(cancelChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)

	stop := new(bool)
	*stop = false

	// Loop to catch SIGTERM of SIGINT
	go catchSig(stop, cancelChan)

	// Don't start until IMAGE_FILE exists
	fileExist := false
	for !fileExist && !*stop {
		time.Sleep(100*time.Millisecond)
		if _, err := os.Stat(IMAGE_FILE); err == nil {
			fileExist = true
		}
	}

	if *stop {
		os.Exit(0)
	}

	ws := WorkingSet{}
	r, err := ws.Init(IMAGE_FILE)
	if err != nil {
		log.Fatalf("got error %e when initializing WorkingSet", err)
	}

	s := fmt.Sprintf("%dx%d", r.Dx(), r.Dy())

	go ws.PermanentUpdate(FILE_FPS)

	cmd := exec.Command("ffmpeg", "-r", fmt.Sprint(FILE_FPS), "-stream_loop", "-1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-s", s, "-i", "pipe:0", "-pix_fmt", "yuv420p", "-map", "0", "-c:a", "aac", "-c:v", "libx264", "-b:v:0", "800k", "-b:v:1", "300k", "-s:v:1", "1280x720", "-profile:v:1", "baseline", "-profile:v:0", "main", "-bf", "1", "-keyint_min", "120", "-g", "120", "-sc_threshold", "0", "-b_strategy", "0", "-ar:a:1", "22050", "-use_timeline", "1", "-use_template", "1", "-r", fmt.Sprint(STREAM_FPS), "-window_size", "5", "-adaptation_sets", "id=0,streams=v id=1,streams=a", "-f", "dash", DASH_FILE)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	in, _ := cmd.StdinPipe()
	cmd.Start()

	now := time.Now()

	ttw := time.Duration(1000000000 / FILE_FPS)

	nextFrame := now.Add(ttw)

	for !(*stop) {
		ws.Lock()
		in.Write(*ws.BufferToPush)
		ws.Unlock()
		time.Sleep(time.Until(nextFrame))
		nextFrame = nextFrame.Add(ttw)
	}
	in.Close()
	cmd.Process.Signal(syscall.SIGTERM)
	cmd.Wait()
}
