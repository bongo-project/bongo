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

#include <config.h>
#include <string.h>
#include <stdio.h>
#include <xpl.h>
#include <bongoutil.h>
#include <bongostream.h>
#include "streamio.h"
#include "streamiop.h"

#define DEBUG_BONGOSTREAM 0


#define BONGOSTREAM_BUFSIZE 1024

#if DEBUG_BONGOSTREAM
/* Examine a buffer, used to generate warnings in valgrind */
static void 
Examine(const char *data, int size)
{
    int i;
    printf("examining %d bytes\n", size);
    for (i = 0; i < size; i++) {
        if (data[i] == 125) {
            printf("blah");
        }
    }
}

#endif

static int
StreamToMemory(StreamStruct *codec, StreamStruct *next)
{
#if DEBUG_BONGOSTREAM
    printf("appending %lu bytes\n", codec->Len);
    Examine(codec->Start, codec->Len);
#endif

    codec->StreamData = (void *)MemRealloc(codec->StreamData, codec->Len + codec->StreamLength + 1);
    if (codec->StreamData) {
        memcpy((unsigned char *)codec->StreamData + codec->StreamLength, codec->Start, codec->Len);
        codec->StreamLength += codec->Len;
    }

    return(codec->Len);
}

static StreamStruct *
BongoStreamInsertCodec(BongoStream *stream, const char *name, BOOL encode, StreamStruct *last, void *streamData, void *streamData2)
{
    StreamCodecFunc codec;
    StreamStruct *piece = NULL;
   

#if DEBUG_BONGOSTREAM
    printf("looking for %s\n", name);
#endif

    codec = FindCodec((char *)name, encode);
    
    if (codec) {
        
#if DEBUG_BONGOSTREAM
        printf("added %s\n", name);
#endif
        
        piece = MemMalloc0(sizeof(StreamStruct));
        piece->Codec = codec;
        piece->StreamData = streamData;
        piece->StreamData2 = streamData2;
        
        piece->Start = MemMalloc(BONGOSTREAM_BUFSIZE);
        piece->End = piece->Start + BONGOSTREAM_BUFSIZE;
        
        if (last) {
            piece->Next = last->Next;
            last->Next = piece;
        } else {
            if (stream->first) {
                piece->Next = stream->first;
            }
            stream->first = piece;
        }
    }

    return piece;
}

static int
BongoStreamAddCodecInternal(BongoStream *stream, const char *codec, BOOL encode, void *streamData, void *streamData2)
{
    StreamStruct *last;

    /* Want to plug in before memory codec */
    last = stream->first;
    if (last == stream->out) {
        last = NULL;
    } else {
        while (last && last->Next && last->Next != stream->out) {
            last = last->Next;
        }
    }

    if (BongoStreamInsertCodec(stream, codec, encode, last, streamData, streamData2)) {
        return 0;
    } else {
        return 1;
    }
}

int
BongoStreamAddCodec(BongoStream *stream, const char *codec, BOOL encode)
{
    return(BongoStreamAddCodecInternal(stream, codec, encode, NULL, NULL));
}

int
BongoStreamAddCodecWithData(BongoStream *stream, const char *codec, BOOL encode, void *streamData, void *streamData2)
{
    return(BongoStreamAddCodecInternal(stream, codec, encode, streamData, streamData2));
}


BongoStream *
BongoStreamCreate(char **codecs, int numCodecs, BOOL encode)
{
    BongoStream *stream;
    StreamStruct *last;
    int i;

    StreamioInit();

    stream = MemMalloc0(sizeof(BongoStream));
    if (!stream) {
        return stream;
    }

    last = NULL;
    for (i = 0; i < numCodecs; i++) {
        StreamStruct *s = BongoStreamInsertCodec(stream, codecs[i], encode, last, NULL, NULL);
        if (s) {
            last = s;
        }
    }

    stream->out = MemMalloc0(sizeof(StreamStruct));
    stream->out->Codec = StreamToMemory;
    stream->out->Next = NULL;
    stream->out->Start = MemMalloc(BONGOSTREAM_BUFSIZE);
    stream->out->End = stream->out->Start + BONGOSTREAM_BUFSIZE;

    if (last) {
        last->Next = stream->out;
    } else {
        stream->first = stream->out;
    }

    return stream;
}

void
BongoStreamFree(BongoStream *stream)
{
    StreamStruct *p1, *p2;

    if (stream->out->StreamData) {
        MemFree(stream->out->StreamData);
    }

    for (p1 = stream->first; p1 != NULL; p1 = p2) {
        p2 = p1->Next;
        
        MemFree(p1->Start);
        MemFree(p1);
    }
}

void
BongoStreamPut(BongoStream *stream, const char *bytes, int bufsize)
{
    int pos = 0;
    int remaining = bufsize;
    int read = 0;

#if DEBUG_BONGOSTREAM
    Examine(bytes, bufsize);
#endif

    while (remaining > 0) {
        int toCopy = stream->first->End - (stream->first->Start + stream->first->Len);
        if (remaining < toCopy) {
            toCopy = remaining;
        }

        memcpy(stream->first->Start + stream->first->Len, bytes + pos, toCopy);

#if DEBUG_BONGOSTREAM
        printf("pushing %d:\n%s\n", toCopy, MemStrndup(stream->first->Start + stream->first->Len, toCopy));
#endif

        stream->first->Len += toCopy;
        read = stream->first->Codec(stream->first, stream->first->Next);

#if DEBUG_BONGOSTREAM
        printf("stream processed %d\n", read);
#endif

        stream->first->Len -= read;
        memmove(stream->first->Start, stream->first->Start + read, stream->first->Len);

        remaining -= toCopy;
        pos += toCopy;
    }
}


int 
BongoStreamGet(BongoStream *stream, char *bytes, int bufSize)
{
    if (stream->out->StreamLength > (unsigned)bufSize) {
#if DEBUG_BONGOSTREAM
        Examine(stream->out->StreamData, bufSize);
#endif
        memcpy(bytes, stream->out->StreamData, bufSize);
#if DEBUG_BONGOSTREAM
        Examine(bytes, bufSize);
#endif
        memmove(stream->out->StreamData, (char*)stream->out->StreamData + bufSize, stream->out->StreamLength - bufSize);

        stream->out->StreamLength -= bufSize;
#if DEBUG_BONGOSTREAM
        printf("too much data, returning %d\n", bufSize);
#endif
        return bufSize;
    } else {
        int toCopy = stream->out->StreamLength;
#if DEBUG_BONGOSTREAM
        Examine(stream->out->StreamData, toCopy);
#endif
        memcpy(bytes, stream->out->StreamData, toCopy);
#if DEBUG_BONGOSTREAM
        Examine(bytes, toCopy);
#endif

        stream->out->StreamLength = 0;

#if DEBUG_BONGOSTREAM
        printf("enough buffer, returning %d\n", toCopy);
#endif
        return toCopy;
    }
}

void
BongoStreamEos(BongoStream *stream)
{
    int read;

#if DEBUG_BONGOSTREAM
    printf("eos!\n");
#endif
    
    stream->first->EOS = TRUE;
    read = stream->first->Codec(stream->first, stream->first->Next);    
    stream->first->Len -= read;
    memmove(stream->first->Start, stream->first->Start + read, stream->first->Len);    
}

char *
BongoStreamStealOutput(BongoStream *stream)
{
    char *ret;

    ret = stream->out->StreamData;
    if (ret) {
        /* NULL terminate the output (we allocated an extra byte, so it's ok)*/
        ((char*)stream->out->StreamData)[stream->out->StreamLength] = '\0';
    }
    
    stream->out->StreamData = NULL;
    stream->out->StreamLength = 0;

    return ret;
}
