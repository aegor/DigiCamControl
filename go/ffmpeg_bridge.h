#ifndef DICAM_FFMPEG_BRIDGE_H
#define DICAM_FFMPEG_BRIDGE_H
#include <stdint.h>
#include <stddef.h>

typedef struct dc_interrupt dc_interrupt;
typedef struct dc_input dc_input;
typedef struct dc_recorder dc_recorder;
void dc_init(void);
const char *dc_version(void);
void dc_error(int code, char *buffer, size_t size);
int dc_is_eof(int code);
int dc_is_again(int code);
dc_interrupt *dc_interrupt_new(void);
void dc_interrupt_cancel(dc_interrupt *control);
void dc_interrupt_arm(dc_interrupt *control, int64_t microseconds);
void dc_interrupt_free(dc_interrupt *control);
dc_input *dc_input_new(dc_interrupt *control);
int dc_input_open(dc_input *input, const char *url, const char *format, int usb);
int dc_input_read(dc_input *input, const uint8_t **data, int *size);
int dc_input_read_jpeg(dc_input *input, const uint8_t **data, int *size);
const char *dc_input_codec(dc_input *input);
int dc_input_width(dc_input *input);
int dc_input_height(dc_input *input);
void dc_input_free(dc_input *input);
dc_recorder *dc_recorder_new(dc_interrupt *control, const char *path, int *error);
int dc_recorder_write(dc_recorder *recorder, const uint8_t *jpeg, int size);
int dc_recorder_finish(dc_recorder *recorder);
void dc_recorder_free(dc_recorder *recorder);
#endif
