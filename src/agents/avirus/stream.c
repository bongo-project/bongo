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
#include <bongoutil.h>
#include <streamio.h>

#include "avirus.h"

/* StreamIO needs this */
BOOL 
MWHandleNamedTemplate(void *ignored1, unsigned char *ignored2, void *ignored3)
{
    return(FALSE);
}

#if 0
static int 
AVStreamToMemory(StreamStruct *codec, StreamStruct *next)
{
    codec->StreamData = (void *)MemRealloc(codec->StreamData, codec->Len + codec->StreamLength + 1);
    if (codec->StreamData) {
        memcpy((unsigned char *)codec->StreamData + codec->StreamLength, codec->Start, codec->Len);
        codec->StreamLength += codec->Len;
    }

    return(codec->Len);
}
#endif

static int 
AVStreamToFile(StreamStruct *codec, StreamStruct *next)
{

    if (codec->StreamData) {
        fwrite(codec->Start, codec->Len, 1, (FILE *)codec->StreamData);
    }

    return(codec->Len);
}


static int
AVStreamFromNMAP(StreamStruct *codec, StreamStruct *next)
{
    int ccode;
    int size;
    int count = codec->StreamLength;
    unsigned char *out;
    AVClient *client = (AVClient *)codec->StreamData;

    while (count > 0) {
        out = next->Start + next->Len;
        size = next->End - out;

        if (count > size) {
            ccode = NMAPReadCount(client->conn, out, size);
        } else {
            ccode = NMAPReadCount(client->conn, out, count);
        }

        if (ccode != -1) {
            next->Len += ccode;

            count -= ccode;
            if (count <= 0) {
                next->EOS = TRUE;
            }

            out = next->Start + next->Len;
            next->Len -= next->Codec(next, next->Next);

            memmove(next->Start, out - next->Len, next->Len);

            continue;
        }

        return(-1);
    }

    return(codec->StreamLength);
}

int 
StreamAttachmentToFile(AVClient *client, unsigned char *queueID, AVMIME *mime)
{
    int ccode;
    int count;
    FILE *fh;
    StreamStruct nCodec;
    StreamStruct eCodec;
    StreamStruct fCodec;

    /* We need to retrieve the object from the queue and store it in our temp directory */
    if (((ccode = NMAPSendCommandF(client->conn, "QBRAW %s %lu %lu\r\n", queueID, mime->part.start, mime->part.size)) != -1) 
            && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1) 
            && ((ccode == 2021) || (ccode == 2023))) {
        if (QuickCmp(mime->encoding, "quoted-printable") || QuickCmp(mime->encoding, "base64")) {
            memset(&nCodec, 0, sizeof(StreamStruct));
            memset(&eCodec, 0, sizeof(StreamStruct));
            memset(&fCodec, 0, sizeof(StreamStruct));

            nCodec.Codec = AVStreamFromNMAP;
            nCodec.StreamData = (void *)client;
            nCodec.StreamLength = mime->part.size;
            nCodec.Next = &eCodec;

            eCodec.Codec = FindCodec(mime->encoding, FALSE);
            eCodec.Start = MemMalloc(sizeof(unsigned char) * CONN_BUFSIZE);
            if (eCodec.Start) {
                eCodec.End = eCodec.Start + (sizeof(unsigned char) * CONN_BUFSIZE);
                eCodec.Next = &fCodec;
            } else {
                return(0);
            }

            fCodec.Codec = AVStreamToFile;
            fCodec.Start = MemMalloc(sizeof(unsigned char) * CONN_BUFSIZE);
            if (fCodec.Start) {
                fCodec.End = fCodec.Start + (sizeof(unsigned char) * CONN_BUFSIZE);
                fCodec.StreamData = (void *)fopen(client->work, "wb");
            } else {
                MemFree(eCodec.Start);
                return(0);
            }

            if (fCodec.StreamData) {
                ccode = nCodec.Codec(&nCodec, nCodec.Next);
                fclose((FILE *)fCodec.StreamData);

                if (NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE) == 1000) {
                    ccode = mime->part.size;
                } else {
                    ccode = 0;
                }
            } else {
                NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE);

                ccode = 0;
            }

            MemFree(fCodec.Start);
            MemFree(eCodec.Start);

            return(ccode);
        }

        /* Maybe should make sure the encoding is 7bit or 8bit? */
        count = mime->part.size;

        /* Get the file */
        fh = fopen(client->work, "wb");
        if (fh) {
            ccode = ConnReadToFile(client->conn, fh, count);

            fclose(fh);

            if ((ccode != -1) 
                    && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1) 
                    && (ccode == 1000)) {
                return(mime->part.size);
            }
        } else {
#if !defined(RELEASE_BUILD)
            perror("antivirus: File error in StreamAttachmentToFile");
#endif

            /* Trouble; we've got NMAP already sending; read the data anyway */
            while (count > 0) {
                if (count > CONN_BUFSIZE) {
                    ccode = NMAPReadCount(client->conn, client->line, CONN_BUFSIZE);
                } else {
                    ccode = NMAPReadCount(client->conn, client->line, count);
                }

                if (ccode != -1) {
                    count -= ccode;
                    continue;
                }

#if !defined(RELEASE_BUILD)
                perror("antivirus: Network error in StreamAttachmentToFile");
#endif
            }

            NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE);
        }
    }

    return(0);
}
