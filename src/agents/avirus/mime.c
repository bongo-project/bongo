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

#include "avirus.h"

#define ParseMIMELine(m, t, e, n, s, l) \
        { \
            unsigned char *a; \
            unsigned char *b; \
            if ((b = strchr((m), ' ')) != NULL) { \
                *b++ = '\0'; \
                strcpy((t), (m)); \
                if ((a = strchr(b, ' ')) != NULL) { \
                    *a++ = '\0'; \
                    strcat((t), "/"); \
                    strcat((t), b); \
                    if ((b = strchr(a, ' ')) != NULL) { \
                        *b++ = '\0'; \
                        if ((a = strchr(b, '"')) != NULL) { \
                            a[-1] = '\0'; \
                            strcpy((e), b); \
                            if ((b = strchr(++a, '"')) != NULL) { \
                                *b++ = '\0'; \
                                strcpy((n), a); \
                                if ((a = strchr(b, ' ')) != NULL) { \
                                    *a++ = '\0'; \
                                    (s) = atol(b); \
                                    if ((b = strchr(++a, ' ')) != NULL) { \
                                        *b = '\0'; \
                                        (l) = atol(a); \
                                    } \
                                } \
                            } \
                        } \
                    } \
                } \
            } \
        }

static BOOL
AddMIMECacheEntry(AVClient *client, unsigned char *type, unsigned char *encoding, unsigned char *name, unsigned long start, unsigned long size)
{
    unsigned long used = client->mime.used;
    AVMIME *mime;

    if ((used + 1) < client->mime.allocated) {
        mime = &(client->mime.cache[used]);
    } else if ((mime = MemRealloc(client->mime.cache, sizeof(AVMIME) * (MIME_REALLOC_SIZE + client->mime.allocated))) != NULL) {
        client->mime.cache = mime;
        mime = &(client->mime.cache[used]);
    } else {
        return(FALSE);
    }

    mime->type = MemStrdup(type);
    mime->encoding = MemStrdup(encoding);
    mime->fileName = MemStrdup(name);
    mime->part.start = start;
    mime->part.size = size;

    client->mime.used++;

    return(TRUE);
}

void 
ClearMIMECache(AVClient *client)
{
    unsigned long i;
    AVMIME *mime;

    if (client->mime.cache) {
        i = client->mime.used;
        mime = client->mime.cache;

        while (i > 0) {
            if (mime->type) {
                MemFree(mime->type);
            }

            if (mime->encoding) {
                MemFree(mime->encoding);
            }

            if (mime->fileName) {
                MemFree(mime->fileName);
            }

            if (mime->virusName) {
                MemFree(mime->virusName);
            }

            i--;
        }

        MemFree(client->mime.cache);
    }

    client->mime.cache = NULL;
    client->mime.used = 0;
    client->mime.allocated = 0;

    return;
}

int 
LoadMIMECache(AVClient *client)
{
    unsigned long ccode;
    unsigned long start;
    unsigned long size;
    unsigned char type[MIME_TYPE_LEN+MIME_SUBTYPE_LEN+1];
    unsigned char encoding[MIME_ENCODING_LEN+1];
    unsigned char name[MIME_NAME_LEN+1];

    do {
        ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE);
        switch (ccode) {
            case 2002: {
                start = (unsigned long)-1;
                size = (unsigned long)-1;
                ParseMIMELine(client->line, type, encoding, name, start, size);
                
                if (!(start == (unsigned long)-1 || size == (unsigned long)-1)) {
                    AddMIMECacheEntry(client, type, encoding, name, start, size);
                }
                
                break;
            }

            case 2003:
            case 2004: {
                break;
            }

            case 1000: {
                return(1000);
            }

            default: {
                return(-1);
            }
        }
    } while (TRUE);

    return(1000);
}
