package main

import (
	"os"
	"testing"
)

func BenchmarkSpinFrame(b *testing.B) {
	env := &Env{SDRoot: os.Getenv("RR_TEST_SD"), DataDir: b.TempDir()}
	u := NewUI(env, 640, 480)
	demoScene(u, "spin")
	c := NewCanvas(640, 480)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		u.Tick(1.0 / 30)
		u.Draw(c)
	}
}
