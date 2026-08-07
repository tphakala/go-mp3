#ifndef MP3DUMP_H
#define MP3DUMP_H
#include <stdint.h>
#include <stdio.h>
/* One FILE* per stage, opened lazily under the outdir set by mp3dump_init. */
void mp3dump_init(const char *outdir);
void mp3dump_close(void);
void mp3dump_f32(const char *stage, uint32_t tag, const float *p, uint32_t n);
void mp3dump_i32(const char *stage, uint32_t tag, const int32_t *p, uint32_t n);
#endif
