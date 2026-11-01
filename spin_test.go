package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func fakeUI(t *testing.T, n int) *UI {
	env := &Env{SDRoot: t.TempDir(), DataDir: t.TempDir()}
	u := NewUI(env, 640, 480)
	sys := &System{ID: "GBA", Label: "GBA", Name: "Game Boy Advance"}
	var roms []*Rom
	dir := t.TempDir()
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, fmt.Sprintf("%d.gba", i))
		os.WriteFile(p, []byte("rom"), 0o644)
		roms = append(roms, &Rom{Path: p, Label: fmt.Sprint(i), Title: fmt.Sprint(i), Sys: sys})
	}
	u.SetRoms(roms, []*System{sys})
	return u
}

// Spin, wait a random while, stop, and run the clock until the card opens.
func spinOnce(u *UI, hold float64) (*Rom, float64) {
	u.Key(BtnA)
	t := 0.0
	for t < hold {
		u.Tick(1.0 / 30)
		t += 1.0 / 30
	}
	u.Key(BtnB)
	start := t
	for u.scr != scrCard {
		u.Tick(1.0 / 30)
		t += 1.0 / 30
		if t > 60 {
			panic("never landed")
		}
	}
	u.Key(BtnB) // back to the machine
	return u.landed, t - start
}

func TestSpinLandsFairly(t *testing.T) {
	for _, n := range []int{1, 3, 12, 30, 31, 500} {
		u := fakeUI(t, n)
		hits := map[string]int{}
		const runs = 3000
		var maxStop float64
		for i := 0; i < runs; i++ {
			hold := float64(i%7) * 0.13 // includes stopping during spin-up
			r, dur := spinOnce(u, hold)
			if r == nil || r != u.centerRom() || u.pos != math.Round(u.pos) {
				t.Fatalf("n=%d: landed %v centre %v pos %v", n, r, u.centerRom(), u.pos)
			}
			hits[r.Path]++
			maxStop = math.Max(maxStop, dur)
		}
		if u.st.Spins != runs {
			t.Errorf("spins %d", u.st.Spins)
		}
		// every game should come up; none should dominate
		exp := float64(runs) / float64(n)
		worst := 0.0
		for _, c := range hits {
			worst = math.Max(worst, math.Abs(float64(c)-exp)/exp)
		}
		if n <= 31 && (len(hits) != n || worst > 0.5) {
			t.Errorf("n=%d: %d distinct, worst deviation %.0f%%", n, len(hits), worst*100)
		}
		if n == 500 && len(hits) < 485 {
			t.Errorf("n=500: only %d distinct winners", len(hits))
		}
		if maxStop > 7 {
			t.Errorf("n=%d: a stop took %.1fs", n, maxStop)
		}
		t.Logf("n=%d distinct=%d worst=%.0f%% longest stop %.1fs", n, len(hits), worst*100, maxStop)
	}
}

func TestControls(t *testing.T) {
	u := fakeUI(t, 50)
	run := func(sec float64) {
		for i := 0; i < int(sec*30); i++ {
			u.Tick(1.0 / 30)
		}
	}
	// A spins, B stops, and pressing A during the jackpot opens the card
	u.Key(BtnA)
	run(1)
	u.Key(BtnB)
	for u.ph != phLanded {
		run(0.1)
	}
	spins := u.st.Spins
	u.Key(BtnA)
	if u.scr != scrCard || u.ph == phSpinUp {
		t.Fatalf("A on the jackpot should open the card (scr %v ph %v)", u.scr, u.ph)
	}
	if u.st.Spins != spins {
		t.Fatal("A on the jackpot spun again")
	}
	// START never mutes; R2 does
	u.Key(BtnB)
	u.Key(BtnStart) // spins
	if u.ph != phSpinUp {
		t.Fatalf("START on the reel should spin, ph %v", u.ph)
	}
	if u.st.Mute {
		t.Fatal("START muted the sound")
	}
	run(1)
	u.Key(BtnStart) // stops
	for u.scr != scrCard {
		run(0.1)
	}
	u.Key(BtnR2)
	if !u.st.Mute {
		t.Fatal("R2 should mute")
	}
	u.Key(BtnR2)
	run(0.5) // past the card's guard
	// START on the card plays the game
	u.Key(BtnStart)
	if !u.quit && u.exitAt == 0 {
		t.Fatal("START on the card should launch")
	}
	// B during the jackpot stays on the reel
	u = fakeUI(t, 50)
	u.Key(BtnA)
	run(1)
	u.Key(BtnA)
	for u.ph != phLanded {
		run(0.1)
	}
	u.Key(BtnB)
	run(3)
	if u.scr != scrMachine || u.quit {
		t.Fatal("B during the jackpot should leave you on the reel")
	}
}

// The device only redraws when Step says so. The jackpot → card handover
// happens inside a tick, so that frame must be drawn (0.2.2 skipped it and the
// card stayed invisible).
func TestCardAppearsOnScreen(t *testing.T) {
	u := fakeUI(t, 50)
	c := NewCanvas(640, 480)
	shown := scrMachine // what the player sees: the screen as of the last draw
	step := func() {
		if u.Step(1.0 / 30) {
			u.Draw(c)
			shown = u.scr
		}
	}
	u.Key(BtnA)
	u.Draw(c)
	for i := 0; i < 30; i++ {
		step()
	}
	u.Key(BtnB)
	u.Draw(c)
	for i := 0; i < 30*8 && u.scr != scrCard; i++ {
		step()
	}
	if u.scr != scrCard {
		t.Fatal("card never opened")
	}
	if shown != scrCard {
		t.Fatal("card opened but the screen was never redrawn")
	}
	// an A that lands as the card appears does nothing; a later one plays
	u.Key(BtnA)
	if u.quit || u.exitAt > 0 {
		t.Fatal("A pressed as the card appeared launched the game")
	}
	for i := 0; i < 20; i++ {
		step()
	}
	u.Key(BtnA)
	if !u.quit && u.exitAt == 0 {
		t.Fatal("A on the card should launch")
	}
}
