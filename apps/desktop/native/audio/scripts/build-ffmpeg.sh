#!/bin/sh
set -eu

source_dir=${1:?provide the extracted FFmpeg source directory}
prefix=${2:?provide the installation prefix}
jobs=${JOBS:-4}
toolchain=${JASTREAMER_FFMPEG_TOOLCHAIN:-native}

case "$toolchain" in
  native)
    toolchain_args="--enable-rpath"
    ;;
  msvc)
    toolchain_args="--toolchain=msvc --arch=x86_64 --target-os=win64 --extra-cflags=-MT --extra-cxxflags=-MT --extra-ldflags=-Brepro"
    ;;
  *)
    echo "unsupported FFmpeg toolchain: $toolchain" >&2
    exit 2
    ;;
esac

source_dir=$(cd "$source_dir" && pwd)
mkdir -p "$prefix"
prefix=$(cd "$prefix" && pwd)
cd "$source_dir"

# Keep the repository's audio decoder set, build only LGPL libraries, and
# deliberately omit network, device, GPL, nonfree, encoder and program code.
# shellcheck disable=SC2086
./configure \
  --prefix="$prefix" \
  $toolchain_args \
  --disable-everything --disable-autodetect --disable-network \
  --disable-doc --disable-debug --disable-static --enable-shared \
  --disable-programs --disable-avdevice --disable-avfilter --disable-swscale \
  --disable-gpl --disable-nonfree --disable-version3 \
  --enable-avformat --enable-avcodec --enable-avutil --enable-swresample \
  --enable-demuxer=aac,aiff,ape,asf,dsf,iff,flac,mov,mp3,ogg,wav,wv,matroska,pcm_s16le,pcm_s16be,pcm_s32le \
  --enable-decoder=aac,aac_fixed,ac3,alac,ape,flac,mp3,mp3float,opus,vorbis,wavpack,wmav1,wmav2,wmapro,wmalossless,dsd_lsbf,dsd_msbf,dsd_lsbf_planar,dsd_msbf_planar,pcm_alaw,pcm_bluray,pcm_dvd,pcm_f16le,pcm_f24le,pcm_f32be,pcm_f32le,pcm_f64be,pcm_f64le,pcm_lxf,pcm_mulaw,pcm_s16be,pcm_s16be_planar,pcm_s16le,pcm_s16le_planar,pcm_s24be,pcm_s24daud,pcm_s24le,pcm_s24le_planar,pcm_s32be,pcm_s32le,pcm_s32le_planar,pcm_s64be,pcm_s64le,pcm_s8,pcm_s8_planar,pcm_sga,pcm_u16be,pcm_u16le,pcm_u24be,pcm_u24le,pcm_u32be,pcm_u32le,pcm_u8,pcm_vidc \
  --enable-parser=aac,aac_latm,ac3,flac,mpegaudio,opus,vorbis
make -j "$jobs"
make install
