/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001-2005 Novell, Inc. All Rights Reserved.
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
#include <nmlib.h>
#include <bongostream.h>
#include <connio.h>

/****************************************************************************

  From the spec:

  <type> 
    A standard MIME identifier for different document types; e.g. text, 
    multipart, message, or application. 
  <subtype> 
    A more specific identifier within the MIME type. For example, subtypes 
    within the "text" type include plain, richtext, and HTML. 
  <charset> 
    The message character set; e.g. US-ASCII and UTF-8. 
  <encoding> 
    The MIME encoding standard; e.g. 7-bit, 8-bit, Quoted-Printable and 
    Base64. 
  <name> 
    The part's filename. Because the filename is user data and may contain 
    spaces, this parameter must always be in quotes. A null value is 
    indicated by double quotes. 
  <part start byte> 
    The byte at which the current part starts. The value is relative to 
    the beginning of the message body. If the message is not multi-part, 
    NMAP returns a 0. 
  <total part size> 
    The total size of the current part. 
  <part header size> 
    Indicates the message's header size. This value is specific to message 
    rfc822 parts ("message" being the type and "rfc822" being the subtype). 
  <total # of lines> 
    The number of lines in the current part. A line is a defined as a 
    sequence of characters ending in a <CRLF> sequence.

 ****************************************************************************/

#define GetNextToken(end,str,delim)      \
    if (!(end = strchr(str, delim))) {     \
        return 0;                        \
    } 

/* returns non-zero on success */

int
NMAPParseMIMELine (char *line, NmapMimeInfo *mimeinfo)
{ 
    char *a;
    char *b;
    
    GetNextToken(b, line, ' ');
    *b++ = '\0';
    strcpy(mimeinfo->type, line);
    
    GetNextToken(a, b, ' ');
    *a++ = '\0';
    strcpy(mimeinfo->subtype, b);

    GetNextToken(b, a, ' ');
    *b++ = '\0';
    strcpy(mimeinfo->charset, a);

    GetNextToken(a, b, '"');
    a[-1] = '\0';
    strcpy(mimeinfo->encoding, b);

    GetNextToken(b, ++a, '"');
    *b++ = '\0';
    strcpy(mimeinfo->name, a);

    GetNextToken(a, ++b, ' ');
    *a++ = '\0';
    mimeinfo->start = atol(b);

    GetNextToken(b, a, ' ');
    *b++ = '\0';
    mimeinfo->size = atol(a);

    /* ignore header size and total lines */

    return 1;
}

static BOOL
FindToplevelMimePart(Connection *conn, 
                     const char *cmd, 
                     const char *id, 
                     const char *supertype, 
                     const char *subtype, 
                     NmapMimeInfo *info)
{
    int ccode;
    int depth = 0;
    int alternative = 0;
    BOOL foundAttachment;
    char buffer[CONN_BUFSIZE];
    
    foundAttachment = FALSE;
    
    ccode = NMAPRunCommandF(conn, buffer, CONN_BUFSIZE, "%s %s\r\n", cmd, id);
    do {
        switch (ccode) {
        case 2002 :
            if (foundAttachment) {
                break;
            }
            
            if (NMAPParseMIMELine(buffer, info)) {
                if (!XplStrCaseCmp(info->type, "multipart") || !XplStrCaseCmp(info->type, "message")) {
                    depth++;
                    if (!XplStrCaseCmp(info->subtype, "alternative")) {
                        alternative++;
                    }
                } else if ((depth < 1 
                            || (depth < 2 && alternative == 1))
                           && (!XplStrCaseCmp(info->type, supertype) && (!subtype || !XplStrCaseCmp(info->subtype, subtype)))) {
                    foundAttachment = TRUE;
                }
            }
            break;
        case 2004 :
            alternative--;
            /* Fall through */
        case 2003 :
            depth--;
            break;
        default :
            break;
        }
        ccode = NMAPReadAnswer(conn, buffer, CONN_BUFSIZE, TRUE);
    } while (ccode >= 2002 && ccode <= 2004);

    return foundAttachment;    
}

BOOL
NMAPQueueFindToplevelMimePart(Connection *conn, 
                              const char *id, 
                              const char *supertype, 
                              const char *subtype, 
                              NmapMimeInfo *info)
{
    return FindToplevelMimePart(conn, "QMIME", id, supertype, subtype, info);
}

BOOL
NMAPFindToplevelMimePart(Connection *conn,
                         const char *id, 
                         const char *supertype, 
                         const char *subtype, 
                         NmapMimeInfo *info)
{
    return FindToplevelMimePart(conn, "MIME", id, supertype, subtype, info);
}


static char *
ReadMimePartUTF8(Connection *conn,
                 const char *id,
                 NmapMimeInfo *info,
                 const char *cmd,
                 int maxLen)
{
    BongoStream *stream;
    char *ret;
    char buffer[CONN_BUFSIZE];
    BOOL success;
    int toRequest;
    int ccode;
    
    stream = BongoStreamCreate(NULL, 0, FALSE);
    if (!stream) {
        return NULL;
    }
    
    BongoStreamAddCodec(stream, info->encoding, FALSE);
    
    toRequest = info->size;
    if (maxLen > 0 && maxLen < info->size) {
        toRequest = maxLen;
    }

    printf("asking for %s\n", id);

    ccode = NMAPRunCommandF(conn, 
                            buffer, 
                            CONN_BUFSIZE,
                            "%s %s %d %d\r\n",
                            cmd, id, info->start, toRequest);
    
    success = FALSE;
    printf("ccode was %d\n", ccode);
    
    if (ccode == 2021 || ccode == 2023) {
        unsigned long remaining;
        int toRead;
        
        remaining = atol(buffer);
        printf("%d bytes remaining (buffer was %s)\n", remaining, buffer);
        
        while (remaining > 0) {
            if (remaining > CONN_BUFSIZE) {
                toRead = CONN_BUFSIZE;
            } else {
                toRead = remaining;
            }
            
            ccode = NMAPReadCount(conn, buffer, toRead);
            printf("read %d bytes\n", ccode);

            if (ccode <= 0) {
                break;
            }
            
            BongoStreamPut(stream, buffer, ccode);
            
            remaining -= ccode;
        }
        
        if (remaining == 0) {
            BongoStreamEos(stream);
            success = TRUE;
        }
    }
    
    if (success) {
        ret = BongoStreamStealOutput(stream);
    } else {
        ret = NULL;
    }
    
    BongoStreamFree(stream);
    
    return ret;
}

char *
NMAPReadMimePartUTF8(Connection *conn,
                     const char *guid,
                     NmapMimeInfo *info,
                     int maxRead)
{
    return ReadMimePartUTF8(conn, guid, info, "READ", maxRead);
}

char *
NMAPQueueReadMimePartUTF8(Connection *conn,
                          const char *qID,
                          NmapMimeInfo *info,
                          int maxRead)
{
    return ReadMimePartUTF8(conn, qID, info, "QBRAW", maxRead);
}





