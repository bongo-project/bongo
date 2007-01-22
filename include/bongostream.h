/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001 Novell, Inc. All Rights Reserved.
 * 
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2 of the GNU General Public License
 * as published by the Free Software Foundation.
 * 
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 * 
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, contact Novell, Inc.
 * 
 * To contact Novell about this file by physical or electronic mail, you 
 * may find current contact information at www.novell.com.
 * </Novell-copyright>
 ****************************************************************************/

/* A small wrapper around streamio that's more amenable to binding in
 * other languages */

#ifndef BONGOSTREAM_H
#define BONGOSTREAM_H

XPL_BEGIN_C_LINKAGE

#include <streamio.h>

typedef struct {
    StreamStruct *first;
    StreamStruct *out;
} BongoStream;

BongoStream *BongoStreamCreate(char **codecs, int numCodecs, BOOL encode);
int BongoStreamAddCodec(BongoStream *stream, const char *codec, BOOL encode);
int BongoStreamAddCodecWithData(BongoStream *stream, const char *codec, BOOL encode, void *streamData, void *streamData2);
void BongoStreamFree(BongoStream *stream);
void BongoStreamPut(BongoStream *stream, const char *bytes, int bufsize);
int BongoStreamGet(BongoStream *stream, char *bytes, int bufSize);
void BongoStreamEos(BongoStream *stream);
char *BongoStreamStealOutput(BongoStream *stream);

typedef long (* SourceReader)(void *sourceHandle, char *buffer, unsigned long bufferSize);

#define STREAM_SOURCE_FILE 0
#define STREAM_SOURCE_CONN 1
#define STREAM_SOURCE_MEM 2

BOOL BongoStreamSearchMimePart(SourceReader readerFunction, void *source, size_t sourceSize, char *charset, char *encoding, char *subtype, char *searchString);
BOOL BongoStreamSearchRfc822Header(SourceReader readerFunction, void *source, size_t sourceSize, char *headerName, char *substring);
char *BongoStreamGrabRfc822Header(SourceReader readerFunction, void *source, size_t sourceSize, char *headerName);

XPL_END_C_LINKAGE

#endif
