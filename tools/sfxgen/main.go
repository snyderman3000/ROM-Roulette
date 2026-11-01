// sfxgen synthesizes ROM Roulette's sound effects: 8-bit, NES/arcade-style
// square, triangle and noise voices, all original. Output is signed 16-bit
// mono at 44.1 kHz (.raw), the format rrsfx mixes.
//
//	go run ./tools/sfxgen -out package/ROMRoulette/sfx -preview preview.wav
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
)

const rate = 44100

type buf []float64

func secs(s float64) int { return int(s * rate) }

func newBuf(s float64) buf { return make(buf, secs(s)) }

// note converts "C5", "F#4", "Bb3" to Hz.
func note(n string) float64 {
	steps := map[byte]int{'C': -9, 'D': -7, 'E': -5, 'F': -4, 'G': -2, 'A': 0, 'B': 2}
	s := steps[n[0]]
	i := 1
	if n[i] == '#' {
		s++
		i++
	} else if n[i] == 'b' {
		s--
		i++
	}
	oct := int(n[i] - '0')
	return 440 * math.Pow(2, float64(s+(oct-4)*12)/12)
}

type wave int

const (
	sq125 wave = iota // NES duty cycles
	sq25
	sq50
	tri
	noiseLong
	noiseShort
)

// voice renders one oscillator from t0 for dur seconds. freq(t) and amp(t)
// get the time since the voice began. Volume is stepped to 16 levels and the
// triangle to 16 steps, like the NES APU.
func (b buf) voice(t0, dur float64, w wave, freq, amp func(t float64) float64) {
	start := secs(t0)
	n := secs(dur)
	phase := 0.0
	lfsr := uint16(1)
	noiseVal := 1.0
	noiseAcc := 0.0
	for i := 0; i < n && start+i < len(b); i++ {
		t := float64(i) / rate
		f := freq(t)
		a := math.Round(amp(t)*15) / 15
		var v float64
		switch w {
		case sq125, sq25, sq50:
			duty := map[wave]float64{sq125: 0.125, sq25: 0.25, sq50: 0.5}[w]
			if phase < duty {
				v = 1
			} else {
				v = -1
			}
		case tri:
			x := 4*math.Abs(phase-0.5) - 1
			v = math.Round(x*7.5) / 7.5
		case noiseLong, noiseShort:
			noiseAcc += f / rate
			for noiseAcc >= 1 {
				noiseAcc--
				tap := uint16(1)
				if w == noiseShort {
					tap = 6
				}
				bit := (lfsr ^ (lfsr >> tap)) & 1
				lfsr = (lfsr >> 1) | (bit << 14)
				noiseVal = float64(int(lfsr&1)*2 - 1)
			}
			v = noiseVal
		}
		b[start+i] += v * a
		phase += f / rate
		phase -= math.Floor(phase)
	}
}

func flat(v float64) func(float64) float64 { return func(float64) float64 { return v } }

// decay: starts at v, falls linearly to 0 over d seconds.
func decay(v, d float64) func(float64) float64 {
	return func(t float64) float64 { return v * math.Max(0, 1-t/d) }
}

// adsrish: quick attack, hold, release at the end of dur.
func env(v, dur, rel float64) func(float64) float64 {
	return func(t float64) float64 {
		if t > dur-rel {
			return v * math.Max(0, (dur-t)/rel)
		}
		return v
	}
}

func vibrato(f, depth, speed, delay float64) func(float64) float64 {
	return func(t float64) float64 {
		if t < delay {
			return f
		}
		return f * (1 + depth*math.Sin(2*math.Pi*speed*(t-delay)))
	}
}

// seq plays a list of notes back to back on one voice.
func (b buf) seq(t0, step float64, w wave, vol float64, notes ...string) float64 {
	for _, n := range notes {
		if n != "-" {
			b.voice(t0, step, w, flat(note(n)), env(vol, step, step*0.25))
		}
		t0 += step
	}
	return t0
}

// ---------- the sounds ----------

func pull() buf { // coin drop + lever ratchet
	b := newBuf(0.62)
	b.voice(0, 0.07, sq25, flat(note("B5")), flat(0.38))
	b.voice(0.07, 0.42, sq25, flat(note("E6")), decay(0.38, 0.42))
	for i := 0; i < 5; i++ { // ratchet clicks, slowing as the lever travels
		t := 0.10 + float64(i)*0.055 + float64(i*i)*0.004
		b.voice(t, 0.025, noiseShort, flat(9000-float64(i)*900), decay(0.30, 0.025))
	}
	b.voice(0.42, 0.11, tri, func(t float64) float64 { return 140 - t*500 }, decay(0.6, 0.11))
	b.voice(0.42, 0.05, noiseLong, flat(2500), decay(0.25, 0.05))
	return b
}

func tick() buf {
	b := newBuf(0.022)
	b.voice(0, 0.022, sq50, flat(1760), decay(0.20, 0.022))
	b.voice(0, 0.010, noiseShort, flat(16000), decay(0.10, 0.010))
	return b
}

func brake() buf {
	b := newBuf(0.30)
	b.voice(0, 0.28, sq25, func(t float64) float64 { return 700 * math.Pow(0.18, t/0.28) }, decay(0.32, 0.28))
	b.voice(0, 0.12, noiseLong, flat(6000), decay(0.18, 0.12))
	return b
}

func clunk() buf {
	b := newBuf(0.16)
	b.voice(0, 0.15, tri, func(t float64) float64 { return 110 - t*350 }, decay(0.75, 0.15))
	b.voice(0, 0.04, noiseLong, flat(3200), decay(0.35, 0.04))
	return b
}

func jackpot() buf {
	b := newBuf(1.85)
	step := 0.055
	t := b.seq(0, step, sq50, 0.30, "C5", "E5", "G5", "C6", "E5", "G5", "C6", "E6", "G5", "C6", "E6", "G6")
	// held top note with vibrato, a third below for harmony
	b.voice(t, 0.70, sq50, vibrato(note("C7"), 0.012, 7, 0.08), env(0.28, 0.70, 0.35))
	b.voice(t, 0.70, sq125, vibrato(note("G6"), 0.012, 7, 0.08), env(0.18, 0.70, 0.35))
	// triangle bass walking up
	bt := 0.0
	for _, n := range []string{"C3", "G3", "C4"} {
		b.voice(bt, step*4, tri, flat(note(n)), env(0.55, step*4, 0.03))
		bt += step * 4
	}
	b.voice(bt, 0.70, tri, flat(note("C3")), env(0.55, 0.70, 0.30))
	// payout: a shower of coin blips
	coins := []string{"E7", "G7", "C7", "A6", "D7", "G6", "E7", "C7", "G7", "D7", "A6", "E7"}
	for i, n := range coins {
		b.voice(t+0.05+float64(i)*0.07, 0.05, sq25, flat(note(n)), decay(0.14, 0.05))
	}
	return b
}

func blip(n string, d, vol float64) buf {
	b := newBuf(d)
	b.voice(0, d, sq25, flat(note(n)), decay(vol, d))
	return b
}

func move() buf { return blip("A5", 0.035, 0.22) }

func pool() buf {
	b := newBuf(0.09)
	b.seq(0, 0.04, sq25, 0.22, "D6", "G6")
	return b
}

func sel() buf {
	b := newBuf(0.11)
	b.seq(0, 0.045, sq50, 0.24, "E6", "A6")
	return b
}

func back() buf {
	b := newBuf(0.11)
	b.seq(0, 0.045, sq50, 0.22, "A5", "E5")
	return b
}

func fav() buf {
	b := newBuf(0.36)
	t := b.seq(0, 0.035, sq125, 0.26, "C6", "E6", "G6")
	b.voice(t, 0.24, sq125, vibrato(note("C7"), 0.01, 9, 0), decay(0.26, 0.24))
	return b
}

func unfav() buf {
	b := newBuf(0.16)
	b.seq(0, 0.045, sq125, 0.22, "C6", "G5", "C5")
	return b
}

func launch() buf { // power-up sweep, then a chord stab
	b := newBuf(1.05)
	// stepped exponential rise — the classic "level up"
	b.voice(0, 0.62, sq25, func(t float64) float64 {
		k := math.Floor(t/0.02) * 0.02 / 0.62
		return 180 * math.Pow(9, k)
	}, flat(0.28))
	b.voice(0, 0.62, tri, func(t float64) float64 { return 90 * math.Pow(4, t/0.62) }, flat(0.45))
	b.voice(0.62, 0.40, sq50, flat(note("C6")), decay(0.24, 0.40))
	b.voice(0.62, 0.40, sq25, flat(note("G6")), decay(0.18, 0.40))
	b.voice(0.62, 0.40, tri, flat(note("C4")), decay(0.55, 0.40))
	b.voice(0.62, 0.06, noiseShort, flat(12000), decay(0.2, 0.06))
	return b
}

func boot() buf {
	b := newBuf(0.75)
	t := b.seq(0, 0.075, sq50, 0.26, "G5", "C6", "E6", "G6")
	b.voice(t, 0.40, sq50, vibrato(note("C7"), 0.01, 6, 0.1), decay(0.26, 0.40))
	b.voice(0, 0.30, tri, flat(note("C4")), env(0.5, 0.30, 0.03))
	b.voice(0.30, 0.40, tri, flat(note("G3")), decay(0.5, 0.40))
	return b
}

func errorBuzz() buf {
	b := newBuf(0.26)
	b.voice(0, 0.12, sq50, flat(note("A2")), env(0.25, 0.12, 0.02))
	b.voice(0, 0.12, sq50, flat(note("A2")*1.06), env(0.18, 0.12, 0.02))
	b.voice(0.13, 0.12, sq50, flat(note("A2")), env(0.25, 0.12, 0.02))
	b.voice(0.13, 0.12, sq50, flat(note("A2")*1.06), env(0.18, 0.12, 0.02))
	return b
}

var sounds = map[string]func() buf{
	"pull": pull, "tick": tick, "brake": brake, "clunk": clunk, "jackpot": jackpot,
	"move": move, "pool": pool, "select": sel, "back": back, "fav": fav, "unfav": unfav,
	"launch": launch, "boot": boot, "error": errorBuzz,
}

func toPCM(b buf) []byte {
	out := make([]byte, len(b)*2)
	for i, v := range b {
		x := v * 0.62 * 32767 // headroom for overlapping voices
		x = math.Max(-32768, math.Min(32767, x))
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(x)))
	}
	return out
}

func main() {
	out := flag.String("out", "package/ROMRoulette/sfx", "folder for the .raw files")
	preview := flag.String("preview", "", "also write a WAV demo of a full spin")
	flag.Parse()
	os.MkdirAll(*out, 0o755)
	names := make([]string, 0, len(sounds))
	for n := range sounds {
		names = append(names, n)
	}
	sort.Strings(names)
	bank := map[string]buf{}
	for _, n := range names {
		b := sounds[n]()
		bank[n] = b
		if err := os.WriteFile(filepath.Join(*out, n+".raw"), toPCM(b), 0o644); err != nil {
			panic(err)
		}
		fmt.Printf("%-8s %.2fs\n", n, float64(len(b))/rate)
	}
	if *preview != "" {
		writePreview(*preview, bank)
	}
}

// writePreview lays the sounds out the way a session plays them.
func writePreview(path string, bank map[string]buf) {
	mixb := newBuf(15)
	put := func(t float64, n string) {
		s := bank[n]
		o := secs(t)
		for i, v := range s {
			if o+i < len(mixb) {
				mixb[o+i] += v
			}
		}
	}
	put(0.2, "boot")
	put(1.3, "move")
	put(1.6, "pool")
	put(2.0, "pull")
	// reel: same physics as the app (spin up 0.45s to 24 rows/s, brake 2.0s)
	pos, t, last := 0.0, 2.0, 0.0
	for ; t < 2.45; t += 0.001 {
		k := (t - 2.0) / 0.45
		pos += 24 * k * k * 0.001
		if math.Floor(pos) > last {
			last = math.Floor(pos)
			put(t, "tick")
		}
	}
	for ; t < 4.2; t += 0.001 {
		pos += 24 * 0.001
		if math.Floor(pos) > last {
			last = math.Floor(pos)
			put(t, "tick")
		}
	}
	put(t, "brake")
	p0 := pos
	p1 := math.Ceil(p0 + 24*2.0/2)
	dur := 2 * (p1 - p0) / 24
	t0 := t
	for ; t < t0+dur; t += 0.001 {
		e := 1 - (t-t0)/dur
		pos = p0 + (p1-p0)*(1-e*e)
		if math.Floor(pos) > last {
			last = math.Floor(pos)
			put(t, "tick")
		}
	}
	put(t, "clunk")
	put(t+0.4, "jackpot")
	put(t+1.55, "select") // card opens
	put(t+3.2, "fav")
	put(t+4.3, "launch")
	pcm := toPCM(mixb)
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	h := make([]byte, 44)
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+len(pcm)))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], rate)
	binary.LittleEndian.PutUint32(h[28:], rate*2)
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(len(pcm)))
	f.Write(h)
	f.Write(pcm)
}
