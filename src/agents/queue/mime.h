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

#ifndef MIME_H
#define MIME_H

#define MAX_HEADER 10240
#define MIME_STRUCT_ALLOC 20

#include "nmap.h"
#include "connio.h"

typedef struct _MIMEPartStruct {
    unsigned char separator[MIME_SEPARATOR_LEN+1];
    unsigned char type[MIME_TYPE_LEN+1];
    unsigned char subtype[MIME_SUBTYPE_LEN+1];
    unsigned char charset[MIME_CHARSET_LEN+1];
    unsigned char encoding[MIME_ENCODING_LEN+1];
    unsigned char name[MIME_NAME_LEN+1];
    long headerStart;
    unsigned long headerLen;
    unsigned long start;
    unsigned long length;
    unsigned long lines;
    unsigned int sepLen;
    unsigned int end;
    BOOL multiMark;
    BOOL rfc822Start;
    BOOL rfc822Mark;
    int rfc822End;
    unsigned int tmpEnd;
    BOOL tmpMultiMark;
    BOOL tmpRFC822Mark;
    BOOL tmpRFC822End;
} MIMEPartStruct;

typedef struct _MIMEReportStruct {
    unsigned long parts;
    MIMEPartStruct *part;
} MIMEReportStruct;

int SendMIME(Connection *conn, MIMEReportStruct *report);
int SendMimeDetails(Connection *conn, MIMEReportStruct *report);
void FreeMIME(MIMEReportStruct *report);
MIMEReportStruct *ParseMIME(FILE *fh, unsigned long headerSize, unsigned long messageSize, long bodyLines, unsigned char *line, unsigned long lineSize);

#endif
