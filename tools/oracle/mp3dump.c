/*
    mp3dump: scalar reference decoder harness built on top of the pinned
    minimp3 (CC0-1.0, see PROVENANCE.md). Decodes an mp3 file, writes
    interleaved float32 PCM to <outdir>/pcm.f32le, and (once hooks are
    added in later tasks) writes one <outdir>/<stage>.dump file per
    enabled dump stage.

    Dump record format, repeated until EOF: tag uint32le, count uint32le,
    payload count*4 bytes (float32le or int32le depending on the call).
*/
#define MINIMP3_IMPLEMENTATION
#include "build/minimp3.h"
#include "dump.h"

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>

#define MP3DUMP_MAX_STAGES 32
#define MP3DUMP_STAGE_NAME_MAX 63

typedef struct
{
    char name[MP3DUMP_STAGE_NAME_MAX + 1];
    FILE *f;
} mp3dump_stage_t;

static mp3dump_stage_t g_stages[MP3DUMP_MAX_STAGES];
static int g_stage_count = 0;
static char g_outdir[4096];

/* Creates outdir and any missing parent directories, mirroring mkdir -p. */
static void mkdir_p(const char *path)
{
    char tmp[4096];
    size_t len = strlen(path);
    if (len == 0 || len >= sizeof(tmp))
    {
        return;
    }
    memcpy(tmp, path, len + 1);
    for (char *p = tmp + 1; *p; p++)
    {
        if (*p == '/')
        {
            *p = '\0';
            mkdir(tmp, 0755);
            *p = '/';
        }
    }
    mkdir(tmp, 0755);
}

void mp3dump_init(const char *outdir)
{
    size_t len = strlen(outdir);
    if (len >= sizeof(g_outdir))
    {
        fprintf(stderr, "mp3dump: outdir path too long: %s\n", outdir);
        exit(1);
    }
    memcpy(g_outdir, outdir, len + 1);
    mkdir_p(g_outdir);
    g_stage_count = 0;
}

static FILE *stage_file(const char *stage)
{
    for (int i = 0; i < g_stage_count; i++)
    {
        if (strcmp(g_stages[i].name, stage) == 0)
        {
            return g_stages[i].f;
        }
    }
    if (g_stage_count >= MP3DUMP_MAX_STAGES)
    {
        fprintf(stderr, "mp3dump: too many dump stages (max %d)\n", MP3DUMP_MAX_STAGES);
        exit(1);
    }
    char path[4096];
    int n = snprintf(path, sizeof(path), "%s/%s.dump", g_outdir, stage);
    if (n < 0 || (size_t)n >= sizeof(path))
    {
        fprintf(stderr, "mp3dump: stage path too long: %s/%s.dump\n", g_outdir, stage);
        exit(1);
    }
    FILE *f = fopen(path, "wb");
    if (!f)
    {
        fprintf(stderr, "mp3dump: cannot open %s: %s\n", path, strerror(errno));
        exit(1);
    }
    strncpy(g_stages[g_stage_count].name, stage, MP3DUMP_STAGE_NAME_MAX);
    g_stages[g_stage_count].name[MP3DUMP_STAGE_NAME_MAX] = '\0';
    g_stages[g_stage_count].f = f;
    return g_stages[g_stage_count++].f;
}

/* Writes one record: tag, count, then count*4 bytes of payload. The host is
   assumed little-endian (x86_64 and arm64, the only build targets), so the
   native uint32_t/float layout already matches the on-disk format. */
static void write_record(FILE *f, uint32_t tag, uint32_t count, const void *payload)
{
    uint32_t hdr[2];
    hdr[0] = tag;
    hdr[1] = count;
    fwrite(hdr, sizeof(uint32_t), 2, f);
    if (count > 0)
    {
        fwrite(payload, sizeof(uint32_t), count, f);
    }
}

void mp3dump_f32(const char *stage, uint32_t tag, const float *p, uint32_t n)
{
    write_record(stage_file(stage), tag, n, p);
}

void mp3dump_i32(const char *stage, uint32_t tag, const int32_t *p, uint32_t n)
{
    write_record(stage_file(stage), tag, n, p);
}

void mp3dump_close(void)
{
    for (int i = 0; i < g_stage_count; i++)
    {
        fclose(g_stages[i].f);
    }
    g_stage_count = 0;
}

int main(int argc, char **argv)
{
    if (argc != 3)
    {
        fprintf(stderr, "usage: %s <in.mp3> <outdir>\n", argv[0]);
        return 1;
    }
    const char *inpath = argv[1];
    const char *outdir = argv[2];

    FILE *in = fopen(inpath, "rb");
    if (!in)
    {
        fprintf(stderr, "mp3dump: cannot open %s: %s\n", inpath, strerror(errno));
        return 1;
    }
    if (fseek(in, 0, SEEK_END) != 0)
    {
        fprintf(stderr, "mp3dump: cannot seek %s: %s\n", inpath, strerror(errno));
        fclose(in);
        return 1;
    }
    long insize = ftell(in);
    if (insize < 0 || fseek(in, 0, SEEK_SET) != 0)
    {
        fprintf(stderr, "mp3dump: cannot determine size of %s\n", inpath);
        fclose(in);
        return 1;
    }

    uint8_t *buf = malloc((size_t)insize > 0 ? (size_t)insize : 1);
    if (!buf)
    {
        fprintf(stderr, "mp3dump: out of memory reading %s\n", inpath);
        fclose(in);
        return 1;
    }
    if (insize > 0 && fread(buf, 1, (size_t)insize, in) != (size_t)insize)
    {
        fprintf(stderr, "mp3dump: short read on %s\n", inpath);
        free(buf);
        fclose(in);
        return 1;
    }
    fclose(in);

    mp3dump_init(outdir);

    char pcmpath[4096];
    int pn = snprintf(pcmpath, sizeof(pcmpath), "%s/pcm.f32le", outdir);
    if (pn < 0 || (size_t)pn >= sizeof(pcmpath))
    {
        fprintf(stderr, "mp3dump: outdir path too long: %s\n", outdir);
        free(buf);
        return 1;
    }
    FILE *pcmf = fopen(pcmpath, "wb");
    if (!pcmf)
    {
        fprintf(stderr, "mp3dump: cannot open %s: %s\n", pcmpath, strerror(errno));
        free(buf);
        return 1;
    }

    mp3dec_t dec;
    mp3dec_init(&dec);
    mp3d_sample_t pcm[MINIMP3_MAX_SAMPLES_PER_FRAME];
    mp3dec_frame_info_t info;

    const uint8_t *pos = buf;
    size_t remaining = (size_t)insize;
    while (remaining > 0)
    {
        int samples = mp3dec_decode_frame(&dec, pos, (int)remaining, pcm, &info);
        if (samples > 0)
        {
            fwrite(pcm, sizeof(float), (size_t)samples * (size_t)info.channels, pcmf);
        }
        if (info.frame_bytes <= 0)
        {
            /* No progress possible: remaining bytes cannot start a frame. */
            break;
        }
        pos += info.frame_bytes;
        remaining -= (size_t)info.frame_bytes;
    }

    fclose(pcmf);
    mp3dump_close();
    free(buf);
    return 0;
}
