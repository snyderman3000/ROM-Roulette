package main

import (
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type screen int

const (
	scrLoading screen = iota
	scrMachine
	scrCard
	scrList
)

type phase int

const (
	phIdle phase = iota
	phSpinUp
	phSpinning
	phStopping
	phSettle
	phLanded
)

const (
	vMax      = 24.0 // rows per second at full spin
	spinUpT   = 0.45 // seconds to reach full speed
	stopT     = 2.0  // seconds the reel takes to brake to a stop
	settleT   = 0.40
	landHold  = 1.15 // jackpot lights before the card opens
	rowH      = 60
	cardGuard = 0.45 // seconds after a card opens before A/X act on it
)

// Reel window geometry (screen coordinates).
const (
	winX, winY, winW, winH = 52, 148, 476, 180
	winMid                 = winY + winH/2
)

type Pool struct {
	Name  string
	Short string
	Match func(*Rom) bool
}

type listItem struct {
	Path, Label, System string
	Time                int64
}

type UI struct {
	env *Env
	st  *State
	W   int
	H   int

	frame int
	t     float64 // seconds since start, advanced by Tick
	quit  bool
	post  chan func()

	scr     screen
	loaded  int
	roms    []*Rom
	systems []*System
	byPath  map[string]*Rom

	pools   []Pool
	poolIdx int
	order   []*Rom       // the pool
	rows    map[int]*Rom // what each reel row shows, filled in as rows scroll into view
	pos     float64

	ph       phase
	phT      float64 // when the current phase began
	v        float64
	stopReq  bool
	p0, p1   float64
	stopDur  float64
	leverT   float64
	landed   *Rom
	lastMove float64 // pos delta of the last tick, for motion blur

	card     *Rom
	cardT    float64 // when the card opened
	cardArt  *image.RGBA
	cardFrom screen

	tab      int // 0 played, 1 favourites
	sel, top int
	items    []listItem

	toast      string
	toastUntil float64

	rng    *rand.Rand
	snd    *Sound
	exitAt float64 // when the launch fanfare ends and the app hands over to Onion

	// fonts
	fMarquee, fBig, fLabel, fChip, fReel, fBody, fSmall, fLCD, fTitle *Font

	bg    *Canvas
	reel  *Canvas
	bulbs []image.Point
	glow  []float64
	glowR int
}

func NewUI(env *Env, w, h int) *UI {
	u := &UI{env: env, W: w, H: h, post: make(chan func(), 16), byPath: map[string]*Rom{},
		rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
	u.st = LoadState(env.DataDir)
	u.fMarquee = loadFont(marqueeTTF, 40)
	u.fBig = loadFont(marqueeTTF, 26)
	u.fLabel = loadFont(marqueeTTF, 15)
	u.fChip = loadFont(marqueeTTF, 12)
	u.fReel = loadFont(bodyTTF, 24)
	u.fBody = loadFont(bodyTTF, 18)
	u.fSmall = loadFont(bodyTTF, 15)
	u.fLCD = loadFont(lcdTTF, 30)
	u.fTitle = loadFont(titleTTF, 21)
	u.reel = NewCanvas(winW, winH)
	u.buildBulbs()
	u.buildBG()
	return u
}

// Start scans the SD card in the background.
func (u *UI) Start() {
	go func() {
		roms, systems := ScanRoms(u.env.SDRoot, func(n int) {
			select {
			case u.post <- func() { u.loaded = n }:
			default:
			}
		})
		u.post <- func() { u.SetRoms(roms, systems) }
	}()
}

func (u *UI) SetRoms(roms []*Rom, systems []*System) {
	u.roms, u.systems = roms, systems
	u.byPath = map[string]*Rom{}
	for _, r := range roms {
		u.byPath[r.Path] = r
	}
	u.pools = []Pool{
		{"All games", "ALL", func(*Rom) bool { return true }},
		{"Favorites", "FAVES", func(r *Rom) bool { return u.st.IsFav(r.Path) }},
		{"Never played", "NEW", func(r *Rom) bool { s := u.st.Stats[r.Path]; return s == nil || s.Played == 0 }},
	}
	for _, s := range systems {
		s := s
		u.pools = append(u.pools, Pool{s.Name, s.Label, func(r *Rom) bool { return r.Sys == s }})
	}
	u.poolIdx = 0
	for i, p := range u.pools {
		if p.Name == u.st.Pool {
			u.poolIdx = i
		}
	}
	u.buildOrder()
	u.scr = scrMachine
	if len(roms) > 0 {
		u.snd.Play("boot")
	} else {
		u.snd.Play("error")
	}
}

func (u *UI) buildOrder() {
	u.order = u.order[:0]
	if len(u.pools) == 0 {
		return
	}
	m := u.pools[u.poolIdx].Match
	for _, r := range u.roms {
		if m(r) {
			u.order = append(u.order, r)
		}
	}
	u.rows = map[int]*Rom{}
	u.pos = 0
}

// at returns the game on reel row i. Rows are drawn at random from the pool the
// first time they're needed (never repeating a neighbour), so the reel never
// has to hold thousands of entries and the stop can be made fair.
func (u *UI) at(i int) *Rom {
	n := len(u.order)
	if n == 0 {
		return nil
	}
	if r, ok := u.rows[i]; ok {
		return r
	}
	var r *Rom
	for try := 0; try < 12; try++ {
		r = u.order[u.rng.Intn(n)]
		if n < 2 || (r != u.rows[i-1] && r != u.rows[i+1]) {
			break
		}
	}
	u.rows[i] = r
	return r
}

func (u *UI) centerRom() *Rom { return u.at(int(math.Round(u.pos))) }

func (u *UI) say(s string) { u.toast, u.toastUntil = s, u.t+2.2 }

func (u *UI) oops(s string) {
	u.say(s)
	u.snd.Play("error")
}

// ---------- reel physics ----------

func (u *UI) startSpin() {
	if len(u.order) == 0 {
		u.oops("NO GAMES IN THIS POOL")
		return
	}
	u.snd.Play("pull")
	u.pos = math.Round(u.pos)
	c := int(u.pos)
	for k := range u.rows { // forget rows that have scrolled away
		if k < c-4 || k > c+4 {
			delete(u.rows, k)
		}
	}
	u.ph, u.phT, u.leverT = phSpinUp, u.t, u.t
	u.stopReq = false
	u.landed = nil
}

// beginStop picks the winner — uniformly from the pool, the moment B is
// pressed — and writes it onto a row the reel hasn't reached yet, then coasts
// there at a rate that matches the reel's current speed.
func (u *UI) beginStop() {
	v := math.Max(u.v, 1)
	dist := math.Max(v*stopT/2, 10) // well past the rows on screen
	p1 := math.Ceil(u.pos + dist)
	k := int(p1)
	delete(u.rows, k)
	n := len(u.order)
	u.rows[k] = u.order[u.rng.Intn(n)]
	u.p0, u.p1 = u.pos, p1
	u.stopDur = 2 * (p1 - u.pos) / v // constant braking from the current speed
	u.ph, u.phT = phStopping, u.t
	u.snd.Play("brake")
}

func (u *UI) Tick(dt float64) {
	u.t += dt
	u.frame++
	before := u.pos
	el := u.t - u.phT
	switch u.ph {
	case phSpinUp:
		k := math.Min(1, el/spinUpT)
		u.v = vMax * k * k
		u.pos += u.v * dt
		if el >= spinUpT {
			u.ph, u.phT, u.v = phSpinning, u.t, vMax
			if u.stopReq {
				u.beginStop()
			}
		}
	case phSpinning:
		u.pos += u.v * dt
	case phStopping:
		x := el / u.stopDur
		if x >= 1 {
			u.pos = u.p1
			u.ph, u.phT, u.v = phSettle, u.t, 0
			u.snd.Play("clunk")
		} else {
			e := 1 - x
			u.pos = u.p0 + (u.p1-u.p0)*(1-e*e)
			u.v = 2 * (u.p1 - u.p0) / u.stopDur * e
		}
	case phSettle:
		if el >= settleT {
			u.pos = u.p1
			u.ph, u.phT = phLanded, u.t
			u.landed = u.centerRom()
			if u.landed != nil {
				u.st.Landed(u.landed)
				u.st.Save()
				u.snd.Play("jackpot")
			}
		} else {
			u.pos = u.p1 + 0.16*math.Sin(el/settleT*math.Pi*2)*math.Exp(-el*7)
		}
	case phLanded:
		if el >= landHold && u.scr == scrMachine && u.landed != nil {
			u.openCard(u.landed, scrMachine)
		}
	}
	u.lastMove = u.pos - before
	// a click for every row that passes the payline
	if (u.ph == phSpinUp || u.ph == phSpinning || u.ph == phStopping) && math.Floor(u.pos) != math.Floor(before) {
		u.snd.Excl("tick")
	}
	if u.exitAt > 0 && u.t >= u.exitAt {
		u.quit = true
	}
}

// Step advances time and reports whether the screen needs redrawing — when
// something is animating, or when the tick itself changed what's on screen
// (the jackpot handing over to the game card, a toast expiring).
func (u *UI) Step(dt float64) bool {
	scr, ph, toast := u.scr, u.ph, u.toast != "" && u.t < u.toastUntil
	u.Tick(dt)
	if u.scr != scr || u.ph != ph || toast != (u.toast != "" && u.t < u.toastUntil) {
		return true
	}
	return u.Animating()
}

func (u *UI) Animating() bool {
	switch u.scr {
	case scrLoading:
		return true
	}
	if u.exitAt > 0 {
		return true
	}
	switch u.scr {
	case scrMachine:
		if u.ph != phIdle {
			return true
		}
		return u.frame%3 == 0 // idle bulb chase at ~10fps
	}
	return u.toast != "" && u.t < u.toastUntil+0.1
}

// ---------- input ----------

func (u *UI) Key(k int) {
	if u.exitAt > 0 {
		return // a game is on its way
	}
	if k == BtnMenu {
		u.quit = true
		return
	}
	if k == BtnR2 && u.scr != scrLoading && u.scr != scrList {
		u.st.Mute = !u.st.Mute
		u.snd.SetMuted(u.st.Mute)
		u.st.Save()
		log.Printf("sound muted=%v (R2)", u.st.Mute)
		if u.st.Mute {
			u.say("SOUND OFF")
		} else {
			u.say("SOUND ON")
			u.snd.Play("select")
		}
		return
	}
	if k == BtnStart && (u.scr == scrMachine || u.scr == scrCard) {
		k = BtnA // START means "go": spin, stop, or play
	}
	switch u.scr {
	case scrMachine:
		u.keyMachine(k)
	case scrCard:
		u.keyCard(k)
	case scrList:
		u.keyList(k)
	}
}

func (u *UI) keyMachine(k int) {
	busy := u.ph != phIdle && u.ph != phLanded
	switch k {
	case BtnA:
		switch u.ph {
		case phLanded:
			// the jackpot is showing: go straight to the game card
			if u.landed != nil {
				u.openCard(u.landed, scrMachine)
				u.snd.Play("select")
			}
		case phIdle:
			u.startSpin()
		case phSpinUp:
			u.stopReq = true
		case phSpinning:
			u.beginStop()
		}
	case BtnB:
		switch u.ph {
		case phSpinUp:
			u.stopReq = true
		case phSpinning:
			u.beginStop()
		case phLanded:
			u.ph = phIdle // stay on the reel; X opens the card later
			u.snd.Play("back")
		case phIdle:
			u.quit = true
		}
	case BtnL1, BtnR1, BtnLeft, BtnRight:
		if busy || len(u.pools) == 0 {
			return
		}
		d := 1
		if k == BtnL1 || k == BtnLeft {
			d = -1
		}
		u.poolIdx = (u.poolIdx + d + len(u.pools)) % len(u.pools)
		u.st.Pool = u.pools[u.poolIdx].Name
		u.st.Save()
		u.buildOrder()
		u.ph = phIdle
		u.snd.Play("pool")
	case BtnUp, BtnDown:
		if busy {
			return
		}
		// nudge the reel by hand to browse neighbours
		if k == BtnUp {
			u.pos = math.Round(u.pos) - 1
		} else {
			u.pos = math.Round(u.pos) + 1
		}
		u.ph = phIdle
		u.snd.Excl("tick")
	case BtnY:
		if busy {
			return
		}
		if r := u.centerRom(); r != nil {
			if u.st.ToggleFav(r) {
				u.say("★ ADDED TO FAVORITES")
				u.snd.Play("fav")
			} else {
				u.say("REMOVED FROM FAVORITES")
				u.snd.Play("unfav")
			}
			u.st.Save()
		}
	case BtnX:
		if !busy {
			if u.ph == phLanded && u.landed != nil {
				u.openCard(u.landed, scrMachine)
				u.snd.Play("select")
				return
			}
			if r := u.centerRom(); r != nil {
				u.openCard(r, scrMachine)
				u.snd.Play("select")
			}
		}
	case BtnSelect:
		if !busy {
			u.openList(0)
			u.snd.Play("select")
		}
	}
}

func (u *UI) openCard(r *Rom, from screen) {
	u.card, u.cardFrom, u.scr = r, from, scrCard
	u.cardT = u.t
	u.cardArt = nil
	if p := r.BoxArt(); p != "" {
		u.cardArt = LoadArt(p, 196, 196)
	}
}

func (u *UI) keyCard(k int) {
	r := u.card
	if u.t-u.cardT < cardGuard && (k == BtnA || k == BtnX) {
		return // a press that arrived with the card isn't a decision about it
	}
	switch k {
	case BtnA:
		if _, err := os.Stat(r.Path); err != nil {
			u.oops("FILE NOT FOUND")
			return
		}
		u.st.Played(r)
		u.st.Save()
		if err := QueueLaunch(u.env, r); err != nil {
			u.oops("COULD NOT LAUNCH: " + err.Error())
			return
		}
		// power-up fanfare, then hand over to Onion
		u.snd.Play("launch")
		u.say("INSERT COIN · GOOD LUCK!")
		u.exitAt = u.t + u.snd.Len("launch") + 0.05
		if u.exitAt <= u.t+0.05 {
			u.quit = true
		}
	case BtnY:
		if u.st.ToggleFav(r) {
			u.say("★ ADDED TO FAVORITES")
			u.snd.Play("fav")
		} else {
			u.say("REMOVED FROM FAVORITES")
			u.snd.Play("unfav")
		}
		u.st.Save()
		if u.cardFrom == scrList {
			u.refreshList()
		}
	case BtnX:
		if u.cardFrom == scrMachine {
			u.scr = scrMachine
			u.startSpin()
		}
	case BtnB:
		u.scr = u.cardFrom
		if u.cardFrom == scrMachine {
			u.ph = phIdle
		}
		u.snd.Play("back")
	}
}

func (u *UI) openList(tab int) {
	u.scr, u.tab, u.sel, u.top = scrList, tab, 0, 0
	u.refreshList()
}

func (u *UI) refreshList() {
	u.items = u.items[:0]
	if u.tab == 0 {
		for _, e := range u.st.History {
			u.items = append(u.items, listItem{e.Path, e.Label, e.System, e.Time})
		}
	} else {
		for _, p := range u.st.FavList() {
			f := u.st.Favorites[p]
			u.items = append(u.items, listItem{p, f.Label, f.System, f.Added})
		}
	}
	if u.sel >= len(u.items) {
		u.sel = len(u.items) - 1
	}
	if u.sel < 0 {
		u.sel = 0
	}
}

const listRows = 9

func (u *UI) keyList(k int) {
	switch k {
	case BtnB:
		u.scr = scrMachine
		u.ph = phIdle
		u.snd.Play("back")
	case BtnL1, BtnR1, BtnLeft, BtnRight:
		u.tab = 1 - u.tab
		u.sel, u.top = 0, 0
		u.refreshList()
		u.snd.Play("pool")
	case BtnUp:
		if u.sel > 0 {
			u.sel--
			u.snd.Play("move")
		}
	case BtnDown:
		if u.sel < len(u.items)-1 {
			u.sel++
			u.snd.Play("move")
		}
	case BtnL2:
		u.sel = max(0, u.sel-listRows)
	case BtnR2:
		u.sel = max(0, min(len(u.items)-1, u.sel+listRows))
	case BtnA:
		if len(u.items) == 0 {
			return
		}
		if r := u.byPath[u.items[u.sel].Path]; r != nil {
			u.openCard(r, scrList)
			u.snd.Play("select")
		} else {
			u.oops("THAT GAME IS NO LONGER ON THE CARD")
		}
	case BtnY:
		if len(u.items) == 0 {
			return
		}
		it := u.items[u.sel]
		if r := u.byPath[it.Path]; r != nil {
			if u.st.ToggleFav(r) {
				u.snd.Play("fav")
			} else {
				u.snd.Play("unfav")
			}
		} else if u.st.IsFav(it.Path) {
			delete(u.st.Favorites, it.Path)
			u.snd.Play("unfav")
		} else {
			u.oops("THAT GAME IS NO LONGER ON THE CARD")
			return
		}
		u.st.Save()
		u.refreshList()
	}
	if u.sel < u.top {
		u.top = u.sel
	}
	if u.sel >= u.top+listRows {
		u.top = u.sel - listRows + 1
	}
}

// ---------- drawing ----------

func (u *UI) Draw(c *Canvas) {
	switch u.scr {
	case scrList:
		u.drawList(c)
	case scrCard:
		u.drawMachine(c)
		c.Blend(0, 0, c.W, c.H, colBlack, 0.62)
		u.drawCard(c)
	default:
		u.drawMachine(c)
	}
	if u.toast != "" && u.t < u.toastUntil {
		w := u.fLabel.Width(u.toast) + 36
		x := (c.W - w) / 2
		c.RoundRect(x-2, 398, w+4, 40, 10, colGold)
		c.RoundRect(x, 400, w, 36, 9, colInk)
		c.TextCenter(u.fLabel, c.W/2, 410, u.toast, colGold)
	}
}

func (u *UI) buildBulbs() {
	x0, y0, x1, y1 := 14, 8, 566, 416
	step := 27.0
	pts := [][2]float64{{float64(x0), float64(y0)}, {float64(x1), float64(y0)}, {float64(x1), float64(y1)}, {float64(x0), float64(y1)}}
	for i := 0; i < 4; i++ {
		a, b := pts[i], pts[(i+1)%4]
		L := math.Hypot(b[0]-a[0], b[1]-a[1])
		n := int(math.Round(L / step))
		for k := 0; k < n; k++ {
			f := float64(k) / float64(n)
			u.bulbs = append(u.bulbs, image.Pt(int(a[0]+(b[0]-a[0])*f), int(a[1]+(b[1]-a[1])*f)))
		}
	}
	u.glowR = 13
	r := u.glowR
	u.glow = make([]float64, (2*r+1)*(2*r+1))
	for y := -r; y <= r; y++ {
		for x := -r; x <= r; x++ {
			d := math.Hypot(float64(x), float64(y)) / float64(r)
			if d < 1 {
				u.glow[(y+r)*(2*r+1)+x+r] = (1 - d) * (1 - d)
			}
		}
	}
}

// buildBG paints everything on the machine screen that never moves.
func (u *UI) buildBG() {
	c := NewCanvas(u.W, u.H)
	c.VGradient(0, 0, c.W, c.H, rgb(0x22, 0x07, 0x0c), colBG)
	for x := 0; x < c.W; x += 18 {
		c.Blend(x, 0, 1, c.H, colRed, 0.05)
	}
	// cabinet
	c.RoundRect(10, 4, 560, 416, 20, colGoldDk)
	c.RoundRect(14, 8, 552, 408, 18, colBrass)
	c.RoundRect(22, 16, 536, 392, 12, colRedDeep)
	c.VGradient(30, 88, 520, 312, rgb(0x6e, 0x10, 0x1c), rgb(0x2a, 0x06, 0x0c))
	// marquee
	c.RoundRect(38, 22, 504, 66, 12, colGold)
	c.RoundRect(41, 25, 498, 60, 10, colBG)
	c.VGradient(44, 28, 492, 54, rgb(0x2a, 0x0a, 0x10), rgb(0x0c, 0x02, 0x04))
	title := "ROM ROULETTE"
	tx := 290 - u.fMarquee.Width(title)/2
	c.Text(u.fMarquee, tx+3, 34, title, colRedDeep)
	c.TextGlow(u.fMarquee, tx, 31, title, colGold)
	// reel window frame
	c.RoundRect(winX-10, winY-10, winW+20, winH+20, 10, colGoldDk)
	c.RoundRect(winX-8, winY-8, winW+16, winH+16, 9, colBrass)
	c.Fill(winX-8, winY-8, winW+16, 3, mix(colBrass, colBulb, 0.5))
	c.Fill(winX-2, winY-2, winW+4, winH+4, colInk)
	// payline arrows
	for i := 0; i < 14; i++ {
		c.Fill(30+i, winMid-(14-i)+1, 1, 2*(14-i)-1, colRed)
		c.Fill(549-i, winMid-(14-i)+1, 1, 2*(14-i)-1, colRed)
	}
	// LCD readouts
	for _, b := range [][4]int{{52, 346, 236, 46}, {296, 346, 112, 46}, {416, 346, 112, 46}} {
		c.RoundRect(b[0]-2, b[1]-2, b[2]+4, b[3]+4, 6, colGoldDk)
		c.RoundRect(b[0], b[1], b[2], b[3], 5, rgb(0x0a, 0x02, 0x02))
	}
	c.Text(u.fChip, 60, 350, "POOL  L/R", colGoldDim)
	c.Text(u.fChip, 304, 350, "IN POOL", colGoldDim)
	c.Text(u.fChip, 424, 350, "SPINS", colGoldDim)
	// lever housing
	c.RoundRect(574, 206, 46, 80, 10, colGoldDk)
	c.RoundRect(577, 209, 40, 74, 8, colBrass)
	c.Fill(594, 216, 6, 60, colInk)
	c.Fill(566, 230, 12, 32, colBrass)
	u.bg = c
}

func (u *UI) drawMachine(c *Canvas) {
	copy(c.Pix, u.bg.Pix)
	u.drawBulbs(c)
	u.drawReel(c)
	u.drawLever(c)
	u.drawStatus(c)

	// LCDs
	pool, count := "—", 0
	if len(u.pools) > 0 {
		pool = strings.ToUpper(u.pools[u.poolIdx].Name)
		count = len(u.order)
	}
	c.Text(u.fLCD, 60, 360, u.fLCD.Ellipsize("◀ "+pool+" ▶", 220), colGold)
	c.TextRight(u.fLCD, 400, 360, commas(count), colRed)
	c.TextRight(u.fLCD, 520, 360, commas(u.st.Spins), colRed)

	// controls
	x := 18
	y := 446
	busy := u.ph != phIdle && u.ph != phLanded
	if u.scr == scrLoading {
		c.TextCenter(u.fSmall, c.W/2, y+3, "ROM ROULETTE · reading your SD card", colPaperDim)
		return
	}
	switch {
	case u.ph == phSpinUp || u.ph == phSpinning:
		x = c.Button(u.fSmall, x, y, "A/B", "STOP THE REEL", colRed)
	case busy:
		c.Text(u.fSmall, x, y+1, "the reel is stopping…", colPaperDim)
	case u.ph == phLanded:
		x = c.Button(u.fSmall, x, y, "A", "OPEN GAME CARD", colRed)
		x = c.Button(u.fSmall, x, y, "B", "STAY ON REEL", colGold)
	default:
		x = c.Button(u.fSmall, x, y, "A", "SPIN", colGold)
		x = c.Button(u.fSmall, x, y, "X", "GAME CARD", colGold)
		x = c.Button(u.fSmall, x, y, "Y", "FAV", colGold)
		x = c.Button(u.fSmall, x, y, "L/R", "POOL", colGold)
		x = c.Button(u.fSmall, x, y, "SEL", "LISTS", colGold)
	}
	snd := "SOUND ON"
	if u.st.Mute {
		snd = "SOUND OFF"
	}
	sw := u.fSmall.Width("R2") + 10 + 6 + u.fSmall.Width(snd)
	col := colGoldDim
	if snd == "SOUND OFF" {
		col = colRed
	}
	c.Button(u.fSmall, c.W-14-sw, y, "R2", snd, col)
}

func (u *UI) drawBulbs(c *Canvas) {
	lit := func(i int) bool { return false }
	t := u.t
	switch {
	case u.scr == scrLoading:
		s := int(t * 18)
		lit = func(i int) bool { return (i+s)%6 < 2 }
	case u.ph == phLanded || u.ph == phSettle:
		on := int(t*8)%2 == 0
		lit = func(i int) bool { return (i%2 == 0) == on }
	case u.ph == phSpinUp || u.ph == phSpinning:
		s := int(t * 30)
		lit = func(i int) bool { return (i+s)%4 == 0 }
	case u.ph == phStopping:
		s := int(u.pos * 1.5)
		lit = func(i int) bool { return (i+s)%4 == 0 }
	default:
		s := int(t * 4)
		lit = func(i int) bool { return (i+s)%3 == 0 }
	}
	for i, p := range u.bulbs {
		if lit(i) {
			u.addGlow(c, p.X, p.Y, colGold, 0.75)
			c.Circle(p.X, p.Y, 5, colBulb, true, 0)
		} else {
			c.Circle(p.X, p.Y, 5, colBulbOff, true, 0)
			c.Set(p.X-2, p.Y-2, mix(colBulbOff, colBulb, 0.4))
		}
	}
}

func (u *UI) addGlow(c *Canvas, cx, cy int, col color.RGBA, k float64) {
	r := u.glowR
	w := 2*r + 1
	for y := -r; y <= r; y++ {
		py := cy + y
		if py < 0 || py >= c.H {
			continue
		}
		for x := -r; x <= r; x++ {
			px := cx + x
			if px < 0 || px >= c.W {
				continue
			}
			g := u.glow[(y+r)*w+x+r] * k
			if g == 0 {
				continue
			}
			o := py*c.Stride + px*4
			c.Pix[o] = addc(c.Pix[o], col.R, g)
			c.Pix[o+1] = addc(c.Pix[o+1], col.G, g)
			c.Pix[o+2] = addc(c.Pix[o+2], col.B, g)
		}
	}
}

func addc(v, a uint8, g float64) uint8 {
	n := int(v) + int(float64(a)*g)
	if n > 255 {
		return 255
	}
	return uint8(n)
}

var chipPalette = []color.RGBA{
	rgb(0xd6, 0x2a, 0x3c), rgb(0x2a, 0x6c, 0xd6), rgb(0x1f, 0x9a, 0x6a), rgb(0x8e, 0x3c, 0xc8),
	rgb(0xe0, 0x7a, 0x14), rgb(0x16, 0x8e, 0xa8), rgb(0xb8, 0x2c, 0x8a), rgb(0x5a, 0x7a, 0x1e),
	rgb(0x3c, 0x4c, 0xa8), rgb(0xa8, 0x50, 0x28),
}

func sysColor(s *System) color.RGBA {
	h := fnv.New32a()
	h.Write([]byte(s.ID))
	return chipPalette[h.Sum32()%uint32(len(chipPalette))]
}

func (u *UI) drawChip(c *Canvas, x, y, w, h int, s *System) {
	col := sysColor(s)
	c.RoundRect(x, y, w, h, 6, col)
	c.Fill(x+4, y+2, w-8, 1, mix(col, colBulb, 0.45))
	lbl := u.fChip.Ellipsize(strings.ToUpper(s.Label), w-8)
	c.TextCenter(u.fChip, x+w/2, y+(h-u.fChip.height)/2+1, lbl, colBulb)
}

func (u *UI) drawReel(c *Canvas) {
	r := u.reel
	cream := colCream
	cream2 := rgb(0xea, 0xdf, 0xc4)
	r.Fill(0, 0, r.W, r.H, cream)
	if u.scr == scrLoading || len(u.order) == 0 {
		msg, sub := "COUNTING CHIPS…", fmt.Sprintf("%s games found so far", commas(u.loaded))
		if u.scr != scrLoading {
			if len(u.roms) == 0 {
				msg, sub = "NO ROMS FOUND", "Add games under Roms/ and install their emulators"
			} else {
				msg, sub = "NOTHING IN THIS POOL", "Press L/R to pick another pool"
			}
		}
		r.TextCenter(u.fBig, r.W/2, r.H/2-34, msg, colRedDeep)
		r.TextCenter(u.fSmall, r.W/2, r.H/2+8, sub, colInkDim)
	} else {
		base := int(math.Floor(u.pos))
		speed := math.Abs(u.v)
		fade := math.Min(0.75, speed/vMax*0.8)
		for i := base - 2; i <= base+3; i++ {
			y := r.H/2 + int(math.Round((float64(i)-u.pos)*rowH)) - rowH/2
			if y > r.H || y+rowH < 0 {
				continue
			}
			rom := u.at(i)
			bg := cream
			if ((i%2)+2)%2 == 1 {
				bg = cream2
			}
			r.Fill(0, y, r.W, rowH, bg)
			r.Fill(0, y+rowH-1, r.W, 1, rgb(0xcf, 0xc0, 0xa0))
			u.drawChip(r, 12, y+17, 66, 26, rom.Sys)
			ink := mix(colInk, bg, fade)
			dim := mix(colInkDim, bg, fade)
			r.Text(u.fReel, 92, y+6, u.fReel.Ellipsize(rom.Title, r.W-92-40), ink)
			sub := rom.Sys.Name
			if len(rom.Tags) > 0 {
				sub += " · " + strings.Join(rom.Tags, ", ")
			}
			r.Text(u.fSmall, 93, y+36, u.fSmall.Ellipsize(sub, r.W-93-40), dim)
			if u.st.IsFav(rom.Path) {
				r.Star(r.W-22, y+rowH/2, 11, mix(colRed, bg, fade))
			}
		}
		// motion smear at speed
		if s := int(math.Abs(u.lastMove) * rowH * 0.6); s >= 2 {
			smear(r, min(s, 22))
		}
	}
	// cylinder shading
	for y := 0; y < r.H; y++ {
		d := math.Abs(float64(y-r.H/2)) / float64(r.H/2)
		if a := 0.62 * d * d * d; a > 0.01 {
			r.Blend(0, y, r.W, 1, rgb(0x20, 0x08, 0x08), a)
		}
	}
	r.Blend(0, 6, r.W, 22, colBulb, 0.10)
	c.DrawArt(r.RGBA, winX, winY)

	// payline frame around the centre row
	fc := colGold
	if u.ph == phLanded || u.ph == phSettle {
		if int(u.t*8)%2 == 0 {
			fc = colRed
		}
		c.Border(winX-1, winMid-rowH/2-3, winW+2, rowH+6, 4, fc)
	} else {
		c.Border(winX-1, winMid-rowH/2-2, winW+2, rowH+4, 2, mix(fc, colInk, 0.25))
	}
}

var (
	smearSrc []byte
	smearSum []int32
)

// smear is a vertical box blur of height s — cheap motion blur for the
// spinning reel. Works row by row so it stays cache-friendly on the Mini.
func smear(r *Canvas, s int) {
	n, w := r.H, r.W*4
	if len(smearSrc) != len(r.Pix) {
		smearSrc = make([]byte, len(r.Pix))
		smearSum = make([]int32, w)
	}
	src, sum := smearSrc, smearSum
	for i := range sum {
		sum[i] = 0
	}
	half := s / 2
	copy(src, r.Pix)
	for y := -half; y < n; y++ {
		if add := y + half; add < n { // row entering the window
			o := add * r.Stride
			for x := 0; x < w; x++ {
				sum[x] += int32(src[o+x])
			}
		}
		if rem := y - half - 1; rem >= 0 { // row leaving it
			o := rem * r.Stride
			for x := 0; x < w; x++ {
				sum[x] -= int32(src[o+x])
			}
		}
		if y < 0 {
			continue
		}
		cnt := min(n-1, y+half) - max(0, y-half) + 1
		inv := int32(65536 / cnt) // multiply instead of divide: the Mini's A7 divides slowly
		o := y * r.Stride
		for x := 0; x < w; x += 4 {
			r.Pix[o+x] = uint8(sum[x] * inv >> 16)
			r.Pix[o+x+1] = uint8(sum[x+1] * inv >> 16)
			r.Pix[o+x+2] = uint8(sum[x+2] * inv >> 16)
		}
	}
}

func (u *UI) drawLever(c *Canvas) {
	const px, py, L = 597, 246, 128
	phi := 0.0
	if el := u.t - u.leverT; u.leverT > 0 && el < 0.6 {
		if el < 0.22 {
			k := el / 0.22
			phi = math.Pi * k * k
		} else {
			k := (el - 0.22) / 0.38
			phi = math.Pi * (1 - k) * (1 - k) * (1 + 2*k)
		}
	}
	ky := py - int(L*math.Cos(phi))
	kr := 15 + int(6*math.Sin(phi))
	top, bot := min(ky, py), max(ky, py)
	c.Fill(px-4, top, 8, bot-top, rgb(0x9a, 0x9a, 0xa4))
	c.Fill(px-4, top, 3, bot-top, rgb(0xe8, 0xe8, 0xf0))
	c.Fill(px+2, top, 2, bot-top, rgb(0x5a, 0x5a, 0x66))
	c.Circle(px, py, 9, colGoldDk, true, 0)
	c.Circle(px, py, 6, colBrass, true, 0)
	c.Circle(px+2, ky+3, kr, rgb(0x40, 0x04, 0x0a), true, 0)
	c.Circle(px, ky, kr, colRed, true, 0)
	c.Circle(px-kr/3, ky-kr/3, kr/3, rgb(0xff, 0x9a, 0xa0), true, 0)
}

func (u *UI) drawStatus(c *Canvas) {
	cx, y := 290, 102
	var s string
	col := colGold
	blink := int(u.t*2.5)%2 == 0
	switch {
	case u.scr == scrLoading:
		s = "SHUFFLING THE DECK"
	case len(u.order) == 0:
		s, blink = "", true
	case u.ph == phSpinUp || u.ph == phSpinning:
		s, col = "PRESS B TO STOP", colBulb
		blink = int(u.t*4)%2 == 0
	case u.ph == phStopping || u.ph == phSettle:
		s, blink = "GOOD LUCK…", true
	case u.ph == phLanded:
		s, blink = "★ JACKPOT ★", true
		if int(u.t*8)%2 == 0 {
			col = colBulb
		}
	default:
		s = "PULL THE LEVER · PRESS A"
	}
	if s != "" && blink {
		c.Text(u.fBig, cx-u.fBig.Width(s)/2+2, y+2, s, colInk)
		c.TextGlow(u.fBig, cx-u.fBig.Width(s)/2, y, s, col)
	}
}

// ---------- card ----------

func (u *UI) drawCard(c *Canvas) {
	r := u.card
	x, y, w, h := 22, 18, 596, 426
	c.RoundRect(x-3, y-3, w+6, h+6, 16, colGold)
	c.RoundRect(x, y, w, h, 14, colCream)
	c.RoundRect(x+6, y+6, w-12, 42, 9, colRed)
	head := "YOU LANDED ON"
	if u.cardFrom == scrList || u.ph != phLanded {
		head = "GAME INFO"
	}
	c.Text(u.fLabel, x+20, y+18, head, colBulb)
	if u.st.IsFav(r.Path) {
		c.Star(x+w-40, y+27, 12, colGold)
	}
	c.RoundRect(x+w-162, y+12, 108, 30, 8, colCream)
	u.drawChip(c, x+w-160, y+14, 104, 26, r.Sys)

	// box art
	ax, ay := x+16, y+62
	c.Fill(ax, ay, 204, 204, colInk)
	c.Fill(ax+2, ay+2, 200, 200, rgb(0x24, 0x10, 0x12))
	if u.cardArt != nil {
		b := u.cardArt.Bounds()
		c.DrawArt(u.cardArt, ax+4+(196-b.Dx())/2, ay+4+(196-b.Dy())/2)
	} else {
		// a stand-in cartridge
		c.RoundRect(ax+36, ay+30, 128, 146, 10, sysColor(r.Sys))
		c.Fill(ax+50, ay+50, 100, 70, colCream)
		c.TextCenter(u.fLabel, ax+100, ay+76, u.fLabel.Ellipsize(strings.ToUpper(r.Sys.Label), 92), colInk)
		for i := 0; i < 6; i++ {
			c.Fill(ax+52+i*17, ay+150, 10, 18, mix(sysColor(r.Sys), colInk, 0.4))
		}
		c.TextCenter(u.fSmall, ax+102, ay+182, "no box art", colPaperDim)
	}

	// details
	dx := x + 238
	dw := x + w - 16 - dx
	ty := y + 60
	lines := u.fTitle.Wrap(r.Title, dw)
	if len(lines) > 3 {
		lines = lines[:3]
		lines[2] = u.fTitle.Ellipsize(lines[2]+"…", dw)
	}
	for _, ln := range lines {
		c.Text(u.fTitle, dx, ty, ln, colInk)
		ty += u.fTitle.height + 2
	}
	ty += 2
	c.Text(u.fBody, dx, ty, u.fBody.Ellipsize(r.Sys.Name, dw), colRed)
	ty += u.fBody.height + 6
	if len(r.Tags) > 0 {
		tx := dx
		for _, t := range r.Tags {
			if tx+u.fSmall.Width(t)+12 > dx+dw {
				break
			}
			tx = c.Chip(u.fSmall, tx, ty, t, colInkDim, false)
		}
		ty += u.fSmall.height + 10
	}
	st := u.st.Stats[r.Path]
	if st == nil {
		st = &RomStats{}
	}
	facts := [][2]string{}
	add := func(k, v string) {
		if v != "" {
			facts = append(facts, [2]string{k, v})
		}
	}
	add("GENRE", r.Genre)
	add("YEAR", r.Year)
	add("BY", r.Dev)
	add("PLAYERS", r.Players)
	add("LANDED", fmt.Sprintf("%d× on the reel", st.Landed))
	add("PLAYED", fmt.Sprintf("%d× from here", st.Played))
	if st.Last > 0 {
		add("LAST RUN", when(st.Last))
	}
	add("FILE", filepath.Base(r.Path))
	add("SIZE", humanSize(r.Size))
	for _, f := range facts {
		if ty > y+262 {
			break
		}
		c.Text(u.fChip, dx, ty+3, f[0], colRedDeep)
		c.Text(u.fSmall, dx+78, ty, u.fSmall.Ellipsize(f[1], dw-78), colInk)
		ty += u.fSmall.height + 5
	}

	// description
	dy := y + 280
	c.HLineDashed(x+16, dy-8, w-32, 6, 4, rgb(0xcf, 0xc0, 0xa0))
	desc := r.Desc
	if desc == "" {
		desc = flavor(r)
	}
	dl := u.fSmall.Wrap(desc, w-36)
	for i, ln := range dl {
		if i == 5 {
			break
		}
		if i == 4 && len(dl) > 5 {
			ln = u.fSmall.Ellipsize(ln+" …", w-36)
		}
		c.Text(u.fSmall, x+18, dy+i*(u.fSmall.height+2), ln, colInkDim)
	}

	// buttons
	by := y + h - 36
	c.Fill(x+6, by-8, w-12, 1, rgb(0xcf, 0xc0, 0xa0))
	bx := x + 18
	bx = u.cardButton(c, bx, by, "A", "PLAY GAME", colRed)
	fav := "FAVORITE"
	if u.st.IsFav(r.Path) {
		fav = "UNFAVORITE"
	}
	bx = u.cardButton(c, bx, by, "Y", fav, colGoldDim)
	if u.cardFrom == scrMachine {
		bx = u.cardButton(c, bx, by, "X", "SPIN AGAIN", colGoldDim)
	}
	u.cardButton(c, bx, by, "B", "BACK", colGoldDim)
}

func (u *UI) cardButton(c *Canvas, x, y int, btn, caption string, col color.RGBA) int {
	f := u.fSmall
	bw := max(f.Width(btn)+12, f.height+4)
	c.RoundRect(x, y, bw, f.height+4, 5, col)
	c.Text(f, x+(bw-f.Width(btn))/2, y+2, btn, colBulb)
	x = c.Text(u.fLabel, x+bw+7, y+3, caption, colInk)
	return x + 20
}

// flavor is the fallback blurb when no scraper has written a description.
func flavor(r *Rom) string {
	lines := []string{
		"The reel has spoken. No description on file for this one — the only way to find out is to play it.",
		"Fresh off the reel. No scraped write-up here, so go in blind and see what you've been hoarding.",
		"A wild cartridge appears. No description on file — press A and make your own review.",
		"House rules: once the reel picks it, you give it at least five minutes.",
	}
	h := fnv.New32a()
	h.Write([]byte(r.Path))
	return lines[h.Sum32()%uint32(len(lines))]
}

// ---------- played & favourites list ----------

func (u *UI) drawList(c *Canvas) {
	c.VGradient(0, 0, c.W, c.H, rgb(0x22, 0x07, 0x0c), colBG)
	c.Fill(0, 0, c.W, 56, colRedDeep)
	c.Fill(0, 56, c.W, 2, colGold)
	c.TextGlow(u.fBig, 18, 14, "ROM ROULETTE", colGold)
	tabs := []string{fmt.Sprintf("PLAYED %d", len(u.st.History)), fmt.Sprintf("FAVES %d", len(u.st.Favorites))}
	tx := c.W - 16
	for i := len(tabs) - 1; i >= 0; i-- {
		w := u.fLabel.Width(tabs[i]) + 24
		tx -= w
		if i == u.tab {
			c.RoundRect(tx, 14, w, 30, 8, colGold)
			c.Text(u.fLabel, tx+12, 22, tabs[i], colInk)
		} else {
			c.RoundRect(tx, 14, w, 30, 8, colGoldDk)
			c.RoundRect(tx+2, 16, w-4, 26, 7, colRedDeep)
			c.Text(u.fLabel, tx+12, 22, tabs[i], colGoldDim)
		}
		tx -= 8
	}

	ly := 70
	rh := 39
	if len(u.items) == 0 {
		msg, sub := "NOTHING PLAYED YET", "Games you launch from the reel will show up here."
		if u.tab == 1 {
			msg, sub = "NO FAVORITES YET", "Press Y on the reel or a game card to save one."
		}
		c.TextCenter(u.fBig, c.W/2, 190, msg, colGold)
		c.TextCenter(u.fBody, c.W/2, 232, sub, colPaperDim)
	}
	for i := u.top; i < len(u.items) && i < u.top+listRows; i++ {
		it := u.items[i]
		y := ly + (i-u.top)*rh
		rom := u.byPath[it.Path]
		if i == u.sel {
			c.RoundRect(10, y, c.W-28, rh-3, 8, colGold)
			c.RoundRect(12, y+2, c.W-32, rh-7, 7, colRedDeep)
		}
		if u.st.IsFav(it.Path) {
			c.Star(30, y+rh/2-1, 9, colGold)
		}
		title := it.Label
		ink := colPaper
		if rom != nil {
			title = rom.Title
		} else {
			title, _ = splitTags(it.Label)
			title += "  (missing)"
			ink = colPaperDim
		}
		right := c.W - 34
		date := when(it.Time)
		right -= u.fSmall.Width(date)
		c.Text(u.fSmall, right, y+11, date, colPaperDim)
		right -= 82
		if rom != nil {
			u.drawChip(c, right, y+7, 70, 22, rom.Sys)
		} else {
			c.Text(u.fChip, right+4, y+12, u.fChip.Ellipsize(strings.ToUpper(it.System), 66), colPaperDim)
		}
		c.Text(u.fBody, 48, y+8, u.fBody.Ellipsize(title, right-48-10), ink)
	}
	// scrollbar
	if n := len(u.items); n > listRows {
		th := listRows * rh
		bh := max(20, th*listRows/n)
		by := ly + (th-bh)*u.top/(n-listRows)
		c.Fill(c.W-12, ly, 4, th, colGrid)
		c.Fill(c.W-12, by, 4, bh, colGold)
	}
	c.Fill(0, 436, c.W, 1, colGoldDk)
	x := 18
	x = c.Button(u.fSmall, x, 446, "A", "GAME CARD", colGold)
	x = c.Button(u.fSmall, x, 446, "Y", "FAV", colGold)
	x = c.Button(u.fSmall, x, 446, "L/R", "SWITCH LIST", colGold)
	c.Button(u.fSmall, x, 446, "B", "BACK TO REEL", colGold)
}

// ---------- helpers ----------

func commas(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func humanSize(b int64) string {
	switch {
	case b <= 0:
		return ""
	case b < 1<<10:
		return fmt.Sprintf("%d B", b)
	case b < 1<<20:
		return fmt.Sprintf("%.0f KB", float64(b)/1024)
	case b < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	}
	return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
}

var nowFunc = time.Now

func when(ts int64) string {
	if ts <= 0 {
		return ""
	}
	t := time.Unix(ts, 0)
	now := nowFunc()
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "today " + strings.ToLower(t.Format("3:04pm"))
	case now.Sub(t) < 48*time.Hour && now.Add(-24*time.Hour).Day() == d2:
		return "yesterday"
	case y1 == y2:
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2 2006")
}
