#include "ffmpeg_bridge.h"
#include <errno.h>
#include <stdatomic.h>
#include <string.h>
#include <libavcodec/avcodec.h>
#include <libavdevice/avdevice.h>
#include <libavformat/avformat.h>
#include <libavutil/error.h>
#include <libavutil/mem.h>
#include <libavutil/time.h>
#include <libswscale/swscale.h>

#define DC_MAX_PACKET (16 * 1024 * 1024)
#define DC_MAX_PIXELS (4096 * 2160)

struct dc_interrupt {
    atomic_int cancelled;
    atomic_int_fast64_t deadline;
};
static int dc_interrupted(void *opaque) {
    dc_interrupt *c = opaque;
    int64_t deadline = atomic_load_explicit(&c->deadline, memory_order_relaxed);
    return atomic_load_explicit(&c->cancelled, memory_order_relaxed) ||
           (deadline && av_gettime_relative() >= deadline);
}
void dc_init(void) {
    av_log_set_level(AV_LOG_ERROR);
    avformat_network_init();
    avdevice_register_all();
}
const char *dc_version(void) { return av_version_info(); }
void dc_error(int code, char *buffer, size_t size) { av_strerror(code, buffer, size); }
int dc_is_eof(int code) { return code == AVERROR_EOF; }
int dc_is_again(int code) { return code == AVERROR(EAGAIN); }
dc_interrupt *dc_interrupt_new(void) {
    dc_interrupt *c = av_mallocz(sizeof(*c));
    if (c) { atomic_init(&c->cancelled, 0); atomic_init(&c->deadline, 0); }
    return c;
}
void dc_interrupt_cancel(dc_interrupt *c) { atomic_store_explicit(&c->cancelled, 1, memory_order_relaxed); }
void dc_interrupt_arm(dc_interrupt *c, int64_t us) {
    atomic_store_explicit(&c->deadline, av_gettime_relative() + us, memory_order_relaxed);
}
void dc_interrupt_free(dc_interrupt *c) { av_free(c); }

struct dc_input {
    AVFormatContext *format;
    AVPacket *packet;
    dc_interrupt *control;
    int video;
    AVCodecContext *decoder, *jpeg_encoder;
    AVFrame *decoded, *preview;
    AVPacket *jpeg;
    struct SwsContext *scaler;
    int draining;
};
dc_input *dc_input_new(dc_interrupt *control) {
    dc_input *in = av_mallocz(sizeof(*in));
    if (in) { in->control = control; in->video = -1; }
    return in;
}
int dc_input_open(dc_input *in, const char *url, const char *format, int usb) {
    if (dc_interrupted(in->control)) return AVERROR_EXIT;
    const AVInputFormat *demuxer = NULL;
    if (format && *format) {
        demuxer = av_find_input_format(format);
        if (!demuxer) return AVERROR_DEMUXER_NOT_FOUND;
    }
    in->format = avformat_alloc_context();
    if (!in->format) return AVERROR(ENOMEM);
    in->format->interrupt_callback = (AVIOInterruptCB){dc_interrupted, in->control};
    AVDictionary *options = NULL;
    if (usb) {
        // V4L2 honors AVFMT_FLAG_NONBLOCK: DQBUF returns EAGAIN instead of
        // blocking indefinitely in the driver, so Go can observe cancellation.
        in->format->flags |= AVFMT_FLAG_NONBLOCK;
        av_dict_set(&options, "input_format", "mjpeg", 0);
        av_dict_set(&options, "video_size", "1920x1080", 0);
        av_dict_set(&options, "framerate", "30", 0);
    } else if (!strncmp(url, "rtsp://", 7)) {
        av_dict_set(&options, "rtsp_transport", "tcp", 0);
        av_dict_set(&options, "timeout", "3000000", 0);
        av_dict_set(&options, "analyzeduration", "1000000", 0);
    }
    int ret = avformat_open_input(&in->format, url, demuxer, &options);
    av_dict_free(&options);
    if (ret < 0) return ret;
    if (!usb && (ret = avformat_find_stream_info(in->format, NULL)) < 0) return ret;
    in->video = av_find_best_stream(in->format, AVMEDIA_TYPE_VIDEO, -1, -1, NULL, 0);
    if (in->video < 0) return in->video;
    in->packet = av_packet_alloc();
    return in->packet ? 0 : AVERROR(ENOMEM);
}
int dc_input_read(dc_input *in, const uint8_t **data, int *size) {
    for (;;) {
        if (dc_interrupted(in->control)) return AVERROR_EXIT;
        av_packet_unref(in->packet);
        int ret = av_read_frame(in->format, in->packet);
        if (ret < 0) return ret;
        if (in->packet->stream_index != in->video) continue;
        if (in->packet->size <= 0 || in->packet->size > DC_MAX_PACKET) return AVERROR_INVALIDDATA;
        *data = in->packet->data;
        *size = in->packet->size;
        return 0;
    }
}
static int dc_input_init_decoder(dc_input *in) {
    AVCodecParameters *parameters = in->format->streams[in->video]->codecpar;
    const AVCodec *codec = avcodec_find_decoder(parameters->codec_id);
    if (!codec) return AVERROR_DECODER_NOT_FOUND;
    in->decoder = avcodec_alloc_context3(codec);
    in->decoded = av_frame_alloc();
    in->preview = av_frame_alloc();
    in->jpeg = av_packet_alloc();
    if (!in->decoder || !in->decoded || !in->preview || !in->jpeg) return AVERROR(ENOMEM);
    int ret = avcodec_parameters_to_context(in->decoder, parameters);
    if (ret < 0) return ret;
    in->decoder->max_pixels = DC_MAX_PIXELS;
    in->decoder->thread_count = 1;
    return avcodec_open2(in->decoder, codec, NULL);
}
static int dc_input_encode_jpeg(dc_input *in, const uint8_t **data, int *size) {
    int width = in->decoded->width, height = in->decoded->height;
    if (width <= 0 || height <= 0 || (int64_t)width * height > DC_MAX_PIXELS) return AVERROR_INVALIDDATA;
    if (width > 1280) { height = (int)((int64_t)height * 1280 / width); width = 1280; }
    width = (width + 1) & ~1; height = (height + 1) & ~1;
    int ret;
    if (!in->jpeg_encoder || in->jpeg_encoder->width != width || in->jpeg_encoder->height != height) {
        avcodec_free_context(&in->jpeg_encoder);
        av_frame_unref(in->preview);
        const AVCodec *codec = avcodec_find_encoder(AV_CODEC_ID_MJPEG);
        if (!codec) return AVERROR_ENCODER_NOT_FOUND;
        in->jpeg_encoder = avcodec_alloc_context3(codec);
        if (!in->jpeg_encoder) return AVERROR(ENOMEM);
        in->jpeg_encoder->width = width; in->jpeg_encoder->height = height;
        in->jpeg_encoder->pix_fmt = AV_PIX_FMT_YUVJ420P;
        in->jpeg_encoder->color_range = AVCOL_RANGE_JPEG;
        in->jpeg_encoder->time_base = (AVRational){1, 30};
        in->jpeg_encoder->thread_count = 1;
        in->jpeg_encoder->flags |= AV_CODEC_FLAG_QSCALE;
        in->jpeg_encoder->global_quality = 5 * FF_QP2LAMBDA;
        if ((ret = avcodec_open2(in->jpeg_encoder, codec, NULL)) < 0) return ret;
        in->preview->format = in->jpeg_encoder->pix_fmt;
        in->preview->width = width; in->preview->height = height;
        if ((ret = av_frame_get_buffer(in->preview, 32)) < 0) return ret;
    }
    if ((ret = av_frame_make_writable(in->preview)) < 0) return ret;
    in->scaler = sws_getCachedContext(in->scaler,
        in->decoded->width, in->decoded->height, in->decoded->format,
        width, height, in->jpeg_encoder->pix_fmt, SWS_BILINEAR, NULL, NULL, NULL);
    if (!in->scaler) return AVERROR(ENOMEM);
    ret = sws_scale(in->scaler, (const uint8_t *const *)in->decoded->data,
        in->decoded->linesize, 0, in->decoded->height, in->preview->data, in->preview->linesize);
    if (ret < 0) return ret;
    in->preview->quality = in->jpeg_encoder->global_quality;
    in->preview->pts = AV_NOPTS_VALUE;
    if ((ret = avcodec_send_frame(in->jpeg_encoder, in->preview)) < 0) return ret;
    av_packet_unref(in->jpeg);
    if ((ret = avcodec_receive_packet(in->jpeg_encoder, in->jpeg)) < 0) return ret;
    if (in->jpeg->size <= 0 || in->jpeg->size > DC_MAX_PACKET) return AVERROR_INVALIDDATA;
    *data = in->jpeg->data; *size = in->jpeg->size;
    return 0;
}
int dc_input_read_jpeg(dc_input *in, const uint8_t **data, int *size) {
    if (in->format->streams[in->video]->codecpar->codec_id == AV_CODEC_ID_MJPEG)
        return dc_input_read(in, data, size);
    int ret;
    if (!in->decoder && (ret = dc_input_init_decoder(in)) < 0) return ret;
    for (;;) {
        if (dc_interrupted(in->control)) return AVERROR_EXIT;
        av_frame_unref(in->decoded);
        ret = avcodec_receive_frame(in->decoder, in->decoded);
        if (ret == 0) return dc_input_encode_jpeg(in, data, size);
        if (ret != AVERROR(EAGAIN)) return ret;
        if (in->draining) return AVERROR_EOF;
        const uint8_t *ignored_data; int ignored_size;
        ret = dc_input_read(in, &ignored_data, &ignored_size);
        if (ret == AVERROR_EOF) {
            in->draining = 1;
            ret = avcodec_send_packet(in->decoder, NULL);
        } else if (ret >= 0) {
            ret = avcodec_send_packet(in->decoder, in->packet);
        }
        if (ret < 0) return ret;
    }
}
const char *dc_input_codec(dc_input *in) {
    return avcodec_get_name(in->format->streams[in->video]->codecpar->codec_id);
}
int dc_input_width(dc_input *in) { return in->format->streams[in->video]->codecpar->width; }
int dc_input_height(dc_input *in) { return in->format->streams[in->video]->codecpar->height; }
void dc_input_free(dc_input *in) {
    if (!in) return;
    avcodec_free_context(&in->decoder); avcodec_free_context(&in->jpeg_encoder);
    av_frame_free(&in->decoded); av_frame_free(&in->preview);
    av_packet_free(&in->jpeg); sws_freeContext(in->scaler);
    av_packet_free(&in->packet);
    avformat_close_input(&in->format);
    av_free(in);
}

struct dc_recorder {
    dc_interrupt *control;
    AVCodecContext *decoder, *encoder;
    AVFormatContext *output;
    AVStream *stream;
    AVFrame *decoded, *converted;
    AVPacket *input, *encoded;
    struct SwsContext *scaler;
    char *path;
    int header_written;
    int64_t next_pts;
};
dc_recorder *dc_recorder_new(dc_interrupt *control, const char *path, int *error) {
    *error = AVERROR(ENOMEM);
    dc_recorder *r = av_mallocz(sizeof(*r));
    if (!r) return NULL;
    r->control = control;
    r->path = av_strdup(path);
    const AVCodec *decoder = avcodec_find_decoder(AV_CODEC_ID_MJPEG);
    if (!decoder) { *error = AVERROR_DECODER_NOT_FOUND; goto fail; }
    r->decoder = avcodec_alloc_context3(decoder);
    r->decoded = av_frame_alloc();
    r->converted = av_frame_alloc();
    r->input = av_packet_alloc();
    r->encoded = av_packet_alloc();
    if (!r->path || !r->decoder || !r->decoded || !r->converted || !r->input || !r->encoded) goto fail;
    r->decoder->max_pixels = DC_MAX_PIXELS;
    r->decoder->thread_count = 1;
    *error = avcodec_open2(r->decoder, decoder, NULL);
    if (*error < 0) goto fail;
    return r;
fail:
    dc_recorder_free(r);
    return NULL;
}
static int dc_recorder_open_output(dc_recorder *r) {
    int width = r->decoded->width, height = r->decoded->height;
    if (width <= 0 || height <= 0 || (int64_t)width * height > DC_MAX_PIXELS || (width % 2) || (height % 2)) return AVERROR_INVALIDDATA;
    const AVCodec *codec = avcodec_find_encoder(AV_CODEC_ID_MPEG4);
    if (!codec) return AVERROR_ENCODER_NOT_FOUND;
    int ret = avformat_alloc_output_context2(&r->output, NULL, "mp4", r->path);
    if (ret < 0) return ret;
    r->output->interrupt_callback = (AVIOInterruptCB){dc_interrupted, r->control};
    r->encoder = avcodec_alloc_context3(codec);
    if (!r->encoder) return AVERROR(ENOMEM);
    r->encoder->width = width; r->encoder->height = height;
    r->encoder->pix_fmt = AV_PIX_FMT_YUV420P;
    r->encoder->time_base = (AVRational){1, 30};
    r->encoder->framerate = (AVRational){30, 1};
    r->encoder->gop_size = 30; r->encoder->max_b_frames = 0;
    r->encoder->thread_count = 1;
    r->encoder->flags |= AV_CODEC_FLAG_QSCALE;
    r->encoder->global_quality = 3 * FF_QP2LAMBDA;
    if (r->output->oformat->flags & AVFMT_GLOBALHEADER) r->encoder->flags |= AV_CODEC_FLAG_GLOBAL_HEADER;
    if ((ret = avcodec_open2(r->encoder, codec, NULL)) < 0) return ret;
    r->stream = avformat_new_stream(r->output, NULL);
    if (!r->stream) return AVERROR(ENOMEM);
    r->stream->time_base = r->encoder->time_base;
    if ((ret = avcodec_parameters_from_context(r->stream->codecpar, r->encoder)) < 0) return ret;
    r->converted->format = r->encoder->pix_fmt;
    r->converted->width = width; r->converted->height = height;
    if ((ret = av_frame_get_buffer(r->converted, 32)) < 0) return ret;
    if ((ret = avio_open2(&r->output->pb, r->path, AVIO_FLAG_WRITE, &r->output->interrupt_callback, NULL)) < 0) return ret;
    if ((ret = avformat_write_header(r->output, NULL)) < 0) return ret;
    r->header_written = 1;
    return 0;
}
static int dc_recorder_drain(dc_recorder *r) {
    for (;;) {
        if (dc_interrupted(r->control)) return AVERROR_EXIT;
        int ret = avcodec_receive_packet(r->encoder, r->encoded);
        if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) return 0;
        if (ret < 0) return ret;
        av_packet_rescale_ts(r->encoded, r->encoder->time_base, r->stream->time_base);
        r->encoded->stream_index = r->stream->index;
        r->encoded->duration = av_rescale_q(1, r->encoder->time_base, r->stream->time_base);
        ret = av_interleaved_write_frame(r->output, r->encoded);
        av_packet_unref(r->encoded);
        if (ret < 0) return ret;
    }
}
int dc_recorder_write(dc_recorder *r, const uint8_t *jpeg, int size) {
    if (dc_interrupted(r->control)) return AVERROR_EXIT;
    if (size <= 0 || size > DC_MAX_PACKET) return AVERROR_INVALIDDATA;
    av_packet_unref(r->input);
    int ret = av_new_packet(r->input, size);
    if (ret < 0) return ret;
    // The caller lends Go memory only for this call. Copy it into an AVPacket
    // with FFmpeg-owned padded storage before submitting it to the decoder.
    memcpy(r->input->data, jpeg, size);
    ret = avcodec_send_packet(r->decoder, r->input);
    av_packet_unref(r->input);
    if (ret < 0) return ret;
    av_frame_unref(r->decoded);
    if ((ret = avcodec_receive_frame(r->decoder, r->decoded)) < 0) return ret;
    if (!r->header_written && (ret = dc_recorder_open_output(r)) < 0) return ret;
    if (r->decoded->width != r->encoder->width || r->decoded->height != r->encoder->height) return AVERROR_INVALIDDATA;
    if ((ret = av_frame_make_writable(r->converted)) < 0) return ret;
    r->scaler = sws_getCachedContext(r->scaler, r->decoded->width, r->decoded->height, r->decoded->format,
        r->encoder->width, r->encoder->height, r->encoder->pix_fmt, SWS_BILINEAR, NULL, NULL, NULL);
    if (!r->scaler) return AVERROR(ENOMEM);
    ret = sws_scale(r->scaler, (const uint8_t *const *)r->decoded->data, r->decoded->linesize,
        0, r->decoded->height, r->converted->data, r->converted->linesize);
    if (ret < 0) return ret;
    r->converted->pts = r->next_pts++;
    r->converted->quality = r->encoder->global_quality;
    if ((ret = avcodec_send_frame(r->encoder, r->converted)) < 0) return ret;
    return dc_recorder_drain(r);
}
int dc_recorder_finish(dc_recorder *r) {
    if (!r->header_written) return AVERROR_INVALIDDATA; // No frames: no valid MP4.
    int ret = avcodec_send_frame(r->encoder, NULL);
    if (ret < 0) return ret;
    if ((ret = dc_recorder_drain(r)) < 0) return ret;
    ret = av_write_trailer(r->output);
    int close_ret = avio_closep(&r->output->pb);
    return ret < 0 ? ret : close_ret;
}
void dc_recorder_free(dc_recorder *r) {
    if (!r) return;
    if (r->output) { if (r->output->pb) avio_closep(&r->output->pb); avformat_free_context(r->output); }
    avcodec_free_context(&r->decoder); avcodec_free_context(&r->encoder);
    av_frame_free(&r->decoded); av_frame_free(&r->converted);
    av_packet_free(&r->input); av_packet_free(&r->encoded);
    sws_freeContext(r->scaler); av_free(r->path); av_free(r);
}
