package main

import (
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

const version = "0.2.3"

type Env struct {
	SDRoot  string
	AppDir  string
	DataDir string
}

func newEnv() *Env {
	exe, _ := os.Executable()
	appDir := filepath.Dir(exe)
	sd := os.Getenv("RR_SDROOT")
	if sd == "" {
		sd = "/mnt/SDCARD"
	}
	env := &Env{SDRoot: sd, AppDir: appDir, DataDir: filepath.Join(appDir, "data")}
	if d := os.Getenv("RR_DATA"); d != "" {
		env.DataDir = d
	}
	os.MkdirAll(env.DataDir, 0o755)
	return env
}

var tzRe = regexp.MustCompile(`^UTC([+-])(\d{1,2}):?(\d{2})?$`)

// useOnionTimezone applies the zone set in Onion's Tweaks (.tmp_update/config/.tz,
// a POSIX string like "UTC+06:00" meaning six hours *behind* UTC).
func useOnionTimezone(sd string) {
	b, err := os.ReadFile(filepath.Join(sd, ".tmp_update", "config", ".tz"))
	if err != nil {
		return
	}
	m := tzRe.FindStringSubmatch(string(trimSpace(b)))
	if m == nil {
		return
	}
	h, _ := strconv.Atoi(m[2])
	mm, _ := strconv.Atoi(m[3])
	off := h*3600 + mm*60
	if m[1] == "+" {
		off = -off
	}
	time.Local = time.FixedZone(string(trimSpace(b)), off)
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}

func main() {
	shot := flag.String("shot", "", "render a scene to PNG instead of running (loading|idle|spin|landed|card|played|faves|empty)")
	out := flag.String("out", "shot.png", "PNG path for -shot")
	bench := flag.Int("bench", 0, "with -shot: render N frames and print the average frame time")
	flag.Parse()

	env := newEnv()
	useOnionTimezone(env.SDRoot)

	if *shot != "" {
		c := NewCanvas(640, 480)
		ui := NewUI(env, c.W, c.H)
		demoScene(ui, *shot)
		ui.Draw(c)
		if *bench > 0 {
			t0 := time.Now()
			for i := 0; i < *bench; i++ {
				ui.Tick(1.0 / 30)
				ui.Draw(c)
			}
			fmt.Printf("%s: %.2f ms/frame\n", *shot, float64(time.Since(t0).Microseconds())/1000/float64(*bench))
		}
		f, err := os.Create(*out)
		if err != nil {
			log.Fatal(err)
		}
		png.Encode(f, c)
		f.Close()
		return
	}

	logf, _ := os.OpenFile(filepath.Join(env.DataDir, "romroulette.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if logf != nil {
		log.SetOutput(logf)
	}
	log.Printf("ROM Roulette %s starting, sd=%s", version, env.SDRoot)

	fb, err := OpenFramebuffer()
	if err != nil {
		log.Fatalf("framebuffer: %v", err)
	}
	defer fb.Close()
	c := NewCanvas(fb.W, fb.H)
	keys := make(chan int, 32)
	if err := ReadInput("/dev/input/event0", keys); err != nil {
		log.Fatalf("input: %v", err)
	}
	ui := NewUI(env, c.W, c.H)
	ui.snd = StartSound(env.AppDir)
	ui.snd.Muted = ui.st.Mute
	log.Print(ui.snd)
	defer ui.snd.Close() // releases the audio device before a game starts
	ui.Start()
	const step = 33 * time.Millisecond
	tick := time.NewTicker(step)
	defer tick.Stop()
	last := time.Now()
	dirty := true
	for !ui.quit {
		select {
		case k := <-keys:
			ui.Key(k)
			dirty = true
		case f := <-ui.post:
			f()
			dirty = true
		case now := <-tick.C:
			dt := now.Sub(last).Seconds()
			last = now
			if dt > 0.1 {
				dt = 0.1 // after a stall, don't jump the reel
			}
			if ui.Step(dt) {
				dirty = true
			}
		}
		if dirty {
			ui.Draw(c)
			fb.Present(c)
			dirty = false
		}
	}
	log.Printf("bye")
	fb.Clear()
}
