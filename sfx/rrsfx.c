// rrsfx — ROM Roulette's sound-effect mixer.
//
// ROM Roulette itself is a static Go binary, which OnionOS's libpadsp.so (the
// LD_PRELOAD shim that carries OSS /dev/dsp audio to the audioserver) can't
// hook. This small dynamically-linked helper can: the app starts it with
// LD_PRELOAD pointing at Onion's libpadsp.so and talks to it over stdin.
//
// Usage: rrsfx <dir-of-.raw-files>
//   Each NAME.raw is signed 16-bit little-endian mono at 44100 Hz.
// Commands, one per line on stdin:
//   p NAME   play (several copies may overlap)
//   x NAME   play exclusively (cuts off an earlier copy of NAME)
//   s        stop everything
//   v N      master volume 0..100
// End of input (the app exiting) makes rrsfx exit.
//
// RRSFX_WAV=file.wav writes the mix to a WAV instead of /dev/dsp (desktop tests,
// paced in real time so command timing is kept).
// Not _FILE_OFFSET_BITS=64: that turns open() into open64(), which OnionOS's
// libpadsp.so doesn't hook. Only the directory listing uses the 64-bit call.
#define _LARGEFILE64_SOURCE
#include <dirent.h>
#include <signal.h>
#include <stdarg.h>
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/soundcard.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>

#define RATE 44100
#define FRAMES 512 // per chunk: ~11.6 ms
#define MAX_SFX 48
#define VOICES 10

typedef struct {
    char name[32];
    int16_t *pcm;
    int len;
} sfx;

typedef struct {
    int id; // -1 idle
    int pos;
    unsigned age;
} voice;

static sfx bank[MAX_SFX];
static int nbank;
static voice vo[VOICES];
static unsigned age;
static int master = 70;
static unsigned plays, stops, stalls;

static struct timespec t_start;

static void logmsg(const char *fmt, ...)
{
    struct timespec now;
    clock_gettime(CLOCK_MONOTONIC, &now);
    double t = (now.tv_sec - t_start.tv_sec) + (now.tv_nsec - t_start.tv_nsec) / 1e9;
    char b[256];
    va_list ap;
    va_start(ap, fmt);
    vsnprintf(b, sizeof b, fmt, ap);
    va_end(ap);
    fprintf(stderr, "rrsfx %6.2fs: %s\n", t, b);
}

static void on_crash(int sig) __attribute__((noreturn));
static void on_crash(int sig)
{
    // async-signal-safe: write() only
    const char m[] = "rrsfx: crashed (signal ";
    if (write(2, m, sizeof m - 1) < 0) _exit(1);
    char d[4] = { '0' + sig / 10, '0' + sig % 10, ')', '\n' };
    if (write(2, d, 4) < 0) _exit(1);
    _exit(128 + sig);
}

static int find(const char *name)
{
    for (int i = 0; i < nbank; i++)
        if (strcmp(bank[i].name, name) == 0) return i;
    return -1;
}

static void load(const char *dir)
{
    DIR *d = opendir(dir);
    if (!d) { logmsg("no sound folder %s", dir); return; }
    struct dirent64 *e;
    while ((e = readdir64(d)) && nbank < MAX_SFX) {
        size_t n = strlen(e->d_name);
        if (n < 5 || n - 4 >= sizeof bank[0].name || strcmp(e->d_name + n - 4, ".raw") || e->d_name[0] == '.') continue;
        char p[512];
        snprintf(p, sizeof p, "%s/%s", dir, e->d_name);
        FILE *f = fopen(p, "rb");
        if (!f) continue;
        fseek(f, 0, SEEK_END);
        long sz = ftell(f);
        fseek(f, 0, SEEK_SET);
        if (sz < 2 || sz > 8 << 20) { fclose(f); continue; }
        sfx *s = &bank[nbank];
        s->pcm = malloc(sz);
        s->len = (int)(fread(s->pcm, 1, sz, f) / 2);
        fclose(f);
        memcpy(s->name, e->d_name, n - 4);
        s->name[n - 4] = 0;
        nbank++;
    }
    closedir(d);
    logmsg("%d sounds loaded from %s", nbank, dir);
}

static void play(int id, int exclusive)
{
    if (id < 0) return;
    int slot = -1;
    if (exclusive)
        for (int i = 0; i < VOICES; i++)
            if (vo[i].id == id) slot = i;
    for (int i = 0; slot < 0 && i < VOICES; i++)
        if (vo[i].id < 0) slot = i;
    if (slot < 0) { // steal the oldest
        slot = 0;
        for (int i = 1; i < VOICES; i++)
            if (vo[i].age < vo[slot].age) slot = i;
    }
    vo[slot].id = id;
    vo[slot].pos = 0;
    vo[slot].age = ++age;
}

static char line[256];
static int linelen;

// returns 0 on end of input
static int read_commands(void)
{
    struct pollfd p = { 0, POLLIN, 0 };
    while (poll(&p, 1, 0) > 0) {
        if (p.revents & (POLLHUP | POLLERR) && !(p.revents & POLLIN)) return 0;
        char buf[256];
        ssize_t n = read(0, buf, sizeof buf);
        if (n <= 0) return 0;
        for (ssize_t i = 0; i < n; i++) {
            char c = buf[i];
            if (c != '\n') {
                if (linelen < (int)sizeof line - 1) line[linelen++] = c;
                continue;
            }
            line[linelen] = 0;
            linelen = 0;
            const char *arg = line[0] && line[1] == ' ' ? line + 2 : "";
            switch (line[0]) {
            case 'p': play(find(arg), 0); plays++; break;
            case 'x': play(find(arg), 1); plays++; break;
            case 's': for (int k = 0; k < VOICES; k++) vo[k].id = -1; stops++; break;
            case 'v': master = atoi(arg); if (master < 0) master = 0; if (master > 100) master = 100; break;
            }
        }
    }
    return 1;
}

static void mix(int16_t *out)
{
    int32_t acc[FRAMES];
    memset(acc, 0, sizeof acc);
    for (int v = 0; v < VOICES; v++) {
        if (vo[v].id < 0) continue;
        sfx *s = &bank[vo[v].id];
        int n = s->len - vo[v].pos;
        if (n > FRAMES) n = FRAMES;
        const int16_t *src = s->pcm + vo[v].pos;
        for (int i = 0; i < n; i++) acc[i] += src[i];
        vo[v].pos += n;
        if (vo[v].pos >= s->len) vo[v].id = -1;
    }
    for (int i = 0; i < FRAMES; i++) {
        int32_t x = acc[i] * master / 100;
        if (x > 32767) x = 32767;
        if (x < -32768) x = -32768;
        out[i * 2] = out[i * 2 + 1] = (int16_t)x;
    }
}

static void wav_header(FILE *f, uint32_t data)
{
    uint32_t rate = RATE, br = RATE * 4, riff = data + 36, fmtlen = 16;
    uint16_t pcm = 1, ch = 2, align = 4, bits = 16;
    fseek(f, 0, SEEK_SET);
    fwrite("RIFF", 1, 4, f); fwrite(&riff, 4, 1, f); fwrite("WAVEfmt ", 1, 8, f);
    fwrite(&fmtlen, 4, 1, f); fwrite(&pcm, 2, 1, f); fwrite(&ch, 2, 1, f);
    fwrite(&rate, 4, 1, f); fwrite(&br, 4, 1, f); fwrite(&align, 2, 1, f); fwrite(&bits, 2, 1, f);
    fwrite("data", 1, 4, f); fwrite(&data, 4, 1, f);
    fseek(f, 0, SEEK_END);
}

int main(int argc, char **argv)
{
    if (argc < 2) { fprintf(stderr, "usage: rrsfx DIR\n"); return 2; }
    clock_gettime(CLOCK_MONOTONIC, &t_start);
    signal(SIGSEGV, on_crash);
    signal(SIGBUS, on_crash);
    signal(SIGPIPE, SIG_IGN);
    for (int i = 0; i < VOICES; i++) vo[i].id = -1;
    load(argv[1]);

    const char *wp = getenv("RRSFX_WAV");
    FILE *wav = NULL;
    uint32_t wav_bytes = 0;
    int fd = -1;
    if (wp) {
        wav = fopen(wp, "wb");
        if (wav) wav_header(wav, 0);
    } else {
        // OnionOS's libpadsp.so looks up the real open() only when a program
        // first opens an ordinary file; if /dev/dsp is the first open() it
        // sees, it calls a null pointer and crashes. Open something harmless
        // first. (Programs that read files with open() never notice.)
        int warm = open("/dev/null", O_RDONLY);
        if (warm >= 0) close(warm);
        logmsg("opening /dev/dsp (LD_PRELOAD=%s)", getenv("LD_PRELOAD") ? getenv("LD_PRELOAD") : "none");
        fd = open("/dev/dsp", O_WRONLY);
        if (fd < 0) { logmsg("no /dev/dsp (%s)", strerror(errno)); }
        else {
            int frag = (4 << 16) | 11; // 4 fragments of 2 KB: ~46 ms of buffering
            ioctl(fd, SNDCTL_DSP_SETFRAGMENT, &frag);
            int fmt = AFMT_S16_LE, ch = 2, rate = RATE;
            ioctl(fd, SNDCTL_DSP_SETFMT, &fmt);
            ioctl(fd, SNDCTL_DSP_CHANNELS, &ch);
            ioctl(fd, SNDCTL_DSP_SPEED, &rate);
            logmsg("/dev/dsp ready: fmt %d ch %d rate %d", fmt, ch, rate);
        }
    }

    int16_t out[FRAMES * 2];
    struct timespec next;
    clock_gettime(CLOCK_MONOTONIC, &next);
    for (;;) {
        if (!read_commands()) break;
        mix(out);
        if (fd >= 0) {
            const char *p = (const char *)out;
            size_t left = sizeof out;
            struct timespec w0, w1;
            clock_gettime(CLOCK_MONOTONIC, &w0);
            while (left > 0) {
                ssize_t n = write(fd, p, left);
                if (n <= 0) { usleep(3000); break; }
                p += n; left -= (size_t)n;
            }
            clock_gettime(CLOCK_MONOTONIC, &w1);
            long ms = (w1.tv_sec - w0.tv_sec) * 1000 + (w1.tv_nsec - w0.tv_nsec) / 1000000;
            if (ms > 300 && stalls++ < 20) logmsg("audio output blocked for %ld ms", ms);
            // The audioserver normally paces us by blocking the write. In case
            // it ever doesn't, never run more than ~60 ms ahead of real time.
            next.tv_nsec += (long)((long long)FRAMES * 1000000000LL / RATE);
            while (next.tv_nsec >= 1000000000L) { next.tv_nsec -= 1000000000L; next.tv_sec++; }
            struct timespec now;
            clock_gettime(CLOCK_MONOTONIC, &now);
            double ahead = (next.tv_sec - now.tv_sec) + (next.tv_nsec - now.tv_nsec) / 1e9;
            if (ahead < -0.5) next = now; // fell behind (stall): don't try to catch up
            else if (ahead > 0.06) {
                struct timespec w = { 0, (long)((ahead - 0.06) * 1e9) };
                nanosleep(&w, NULL);
            }
        } else {
            if (wav) { fwrite(out, 1, sizeof out, wav); wav_bytes += sizeof out; }
            // pace like the real device (blocking writes do this there)
            next.tv_nsec += (long)((long long)FRAMES * 1000000000LL / RATE);
            while (next.tv_nsec >= 1000000000L) { next.tv_nsec -= 1000000000L; next.tv_sec++; }
            clock_nanosleep(CLOCK_MONOTONIC, TIMER_ABSTIME, &next, NULL);
        }
    }
    logmsg("input closed, exiting (%u sounds played, %u stops, %u stalls)", plays, stops, stalls);
    if (wav) { wav_header(wav, wav_bytes); fclose(wav); }
    if (fd >= 0) close(fd);
    return 0;
}
