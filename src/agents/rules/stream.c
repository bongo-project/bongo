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

#include "rules.h"

/* StreamIO needs this */
BOOL 
MWHandleNamedTemplate(void *ClientIn, unsigned char *TemplateName, void *ObjectData)
{
    return(FALSE);
}

int 
RulesStreamToMemory(StreamStruct *codec, StreamStruct *next)
{
    codec->StreamData = (void *)MemRealloc(codec->StreamData, codec->Len + codec->StreamLength + 1);
    if (codec->StreamData) {
        memcpy((unsigned char *)codec->StreamData + codec->StreamLength, codec->Start, codec->Len);
        codec->StreamLength += codec->Len;
    }

    return(codec->Len);
}

int 
RulesStreamToFile(StreamStruct *codec, StreamStruct *next)
{
    if (codec->StreamData) {
        fwrite(codec->Start, codec->Len, 1, (FILE *)codec->StreamData);
    }

    return(codec->Len);
}

int
RulesStreamFromNMAP(StreamStruct *codec, StreamStruct *next)
{
    int ccode;
    int size;
    int count = codec->StreamLength;
    unsigned char *out;
    RulesClient *client = (RulesClient *)codec->StreamData;

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
