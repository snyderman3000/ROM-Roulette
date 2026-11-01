package main

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSoundSession plays a whole session in real time through the desktop
// build of rrsfx (RR_SFX_BIN) and checks the recording it writes (RRSFX_WAV).
func TestSoundSession(t *testing.T) {
	bin, wav := os.Getenv("RR_SFX_BIN"), os.Getenv("RRSFX_WAV")
	if bin == "" || wav == "" {
		t.Skip("set RR_SFX_BIN and RRSFX_WAV")
	}
	appDir := t.TempDir()
	os.MkdirAll(filepath.Join(appDir, "data"), 0o755)
	os.Symlink(mustAbs("package/ROMRoulette/sfx"), filepath.Join(appDir, "sfx"))
	snd := StartSound(appDir)
	if snd.in == nil {
		t.Fatal("helper did not start")
	}
	u := fakeUI(t, 200)
	u.snd = snd
	u.Tick(0)
	u.snd.Play("boot")

	type step struct {
		at  float64
		key int
	}
	script := []step{{1.0, BtnDown}, {1.2, BtnDown}, {1.6, BtnA}, {3.6, BtnB}, {9.5, BtnY}, {10.6, BtnA}}
	start := time.Now()
	last := start
	events := map[string]float64{}
	for i := 0; ; {
		now := time.Now()
		el := now.Sub(start).Seconds()
		for i < len(script) && el >= script[i].at {
			u.Key(script[i].key)
			i++
		}
		prev := u.ph
		u.Tick(now.Sub(last).Seconds())
		last = now
		if prev != phLanded && u.ph == phLanded {
			events["landed"] = el
		}
		if u.scr == scrCard && events["card"] == 0 {
			events["card"] = el
		}
		if u.quit {
			events["quit"] = el
			break
		}
		if el > 16 {
			t.Fatal("session never finished")
		}
		time.Sleep(15 * time.Millisecond)
	}
	snd.Close()
	t.Logf("events %v", events)
	if events["quit"] < 10.6+0.9 {
		t.Errorf("quit at %.2fs: should wait for the launch fanfare", events["quit"])
	}

	// the recording should have sound where the session had events and be
	// quiet between them
	b, err := os.ReadFile(wav)
	if err != nil || len(b) < 44 {
		t.Fatal("no recording")
	}
	pcm := b[44:]
	level := func(t0, t1 float64) float64 {
		peak := 0.0
		for i := int(t0 * 44100); i < int(t1*44100) && i*4+1 < len(pcm); i++ {
			v := math.Abs(float64(int16(binary.LittleEndian.Uint16(pcm[i*4:]))))
			peak = math.Max(peak, v)
		}
		return peak
	}
	checks := []struct {
		name   string
		t0, t1 float64
		loud   bool
	}{
		{"boot", 0.0, 0.6, true},
		{"quiet before nudge", 0.85, 0.98, false},
		{"pull + ticks", 1.6, 3.5, true},
		{"jackpot", events["landed"], events["landed"] + 1.2, true},
		{"launch", 10.6, 11.4, true},
	}
	for _, c := range checks {
		if l := level(c.t0, c.t1); (l > 1500) != c.loud {
			t.Errorf("%s (%.1f–%.1fs): peak %.0f", c.name, c.t0, c.t1, l)
		}
	}
}

func mustAbs(p string) string {
	a, _ := filepath.Abs(p)
	return a
}
