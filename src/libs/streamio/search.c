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
#include <xpl.h>
#include <memmgr.h>
#include <bongoutil.h>
#include <bongostream.h>

#define WORK_BUFSIZE 1024

__inline static char *
BongoStreamGrabOutput(BongoStream *stream, SourceReader readerFunction, void *source, size_t sourceSize)
{
    char buffer[WORK_BUFSIZE];
    long bytesLeft = sourceSize;
    int numRead;
    char *output;

    for(;;) {
        if (bytesLeft > WORK_BUFSIZE) {
            numRead = readerFunction(source, buffer, WORK_BUFSIZE); 
        } else {
            numRead = readerFunction(source, buffer, bytesLeft); 
        }
    
        if (numRead < 1) {
            /* an IO error occured */
            BongoStreamFree(stream);
            return(NULL);
        }

        bytesLeft -= numRead;
        if (bytesLeft > 0) {
            BongoStreamPut(stream, buffer, numRead);
            continue;
        }
        break;
    }

    /* end of the stream encountered */
    stream->first->EOS = TRUE;
    BongoStreamPut(stream, buffer, numRead);

    output = BongoStreamStealOutput(stream);
    BongoStreamFree(stream);
    return(output);
}

char *
BongoStreamGrabRfc822Header(SourceReader readerFunction, void *source, size_t sourceSize, char *headerName)
{
    BongoStream *stream;
    char *codec[2];

    codec[0] = "rfc822_fold";
    codec[1] = "rfc822_header_value";

    stream = BongoStreamCreate(codec, 2, FALSE);
    if (!stream) {
        return(FALSE);        
    }

    /* set the headerName in the rfc822header_filter codec */ 
    stream->first->Next->StreamData = headerName;

    return(BongoStreamGrabOutput(stream, readerFunction, source, sourceSize));
}

__inline static BOOL
BongoStreamSearch(BongoStream *stream, SourceReader readerFunction, void *source, size_t sourceSize)
{
    char buffer[WORK_BUFSIZE];
    long bytesLeft = sourceSize;
    int numRead;

    /* now that the stream is set up, run the contents of the source through it */
    for(;;) {
        if (bytesLeft > WORK_BUFSIZE) {
            numRead = readerFunction(source, buffer, WORK_BUFSIZE); 
        } else {
            numRead = readerFunction(source, buffer, bytesLeft); 
        }
    
        if (numRead < 1) {
            /* an IO error occured */
            break;
        }

        bytesLeft -= numRead;
        if (bytesLeft < 1) {
            /* end of the stream encountered */
            stream->first->EOS = TRUE;
            BongoStreamPut(stream, buffer, numRead);
            do {
                numRead = BongoStreamGet(stream, buffer, WORK_BUFSIZE);
                if (numRead != 0) {
                    /* no need to finish processing the stream, we already have a match */
                    BongoStreamFree(stream);
                    return(TRUE);
                }
            } while (stream->first->Len > 0);
            break;
        }

        BongoStreamPut(stream, buffer, numRead);
        numRead = BongoStreamGet(stream, buffer, WORK_BUFSIZE);
        if (numRead != 0) {
            /* no need to finish processing the stream, we already have a match */
            BongoStreamFree(stream);
            return(TRUE);
        }
    }

    BongoStreamFree(stream);
    return(FALSE);
}


BOOL
BongoStreamSearchRfc822Header(SourceReader readerFunction, void *source, size_t sourceSize, char *headerName, char *substring)
{
    BongoStream *stream;
    char *codec[4];

    codec[0] = "rfc822_fold";

    codec[1] = "rfc822_header_value";

    codec[2] = "rfc1522";

    codec[3] = "search_substring";

    stream = BongoStreamCreate(codec, 4, FALSE);
    if (!stream) {
        return(FALSE);        
    }

    /* set the headerName in the rfc822header_filter codec */ 
    stream->first->Next->StreamData = headerName;

    /* set the substring in the search_substring codec */ 
    stream->first->Next->Next->Next->StreamData = substring;

    return(BongoStreamSearch(stream, readerFunction, source, sourceSize));
}


BOOL
BongoStreamSearchMimePart(SourceReader readerFunction, void *source, size_t sourceSize, char *charset, char *encoding, char *subtype, char *substring)
{
    BongoStream *stream;
    int codecCount = 0;
    char *codec[4];

    /* create the stream */
    if (encoding) {
        codec[codecCount] = encoding;
        codecCount++;
    }

    if ((!subtype) || (XplStrCaseCmp(subtype, "html") != 0)) {
        if (charset) {
            codec[codecCount] = charset;
            codecCount++;
        }

        stream = BongoStreamCreate(codec, codecCount, FALSE);
        if (!stream) {
            return(FALSE);        
        }
    } else {
        if (!charset) {
            charset = "us-ascii";
        }

        codec[codecCount] = "html_text";
        codecCount++;

        codec[codecCount] = charset;
        codecCount++;

        
        stream = BongoStreamCreate(codec, codecCount, FALSE);
        if (!stream) {
            return(FALSE);        
        }

        /* tell the html_text codec where to find the charset codec */
        /* which, in this case, happens to be its next codec */
        if (encoding) {
            stream->first->Next->Charset = stream->first->Next->Next;
        } else {
            stream->first->Charset = stream->first->Next;
        }
    }

    if(BongoStreamAddCodecWithData(stream, "search_substring", FALSE, (void *)substring, NULL)) {
        BongoStreamFree(stream);
        return(FALSE);
    }

    return(BongoStreamSearch(stream, readerFunction, source, sourceSize));
}
