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

#include <xpl.h>

#include "stored.h"

XPL_BEGIN_C_LINKAGE

#define MAX_HEADER 10240
#define MIME_STRUCT_ALLOC 20

/* Maximum size for certain fields (MIME RESPONSE) */
#define	MIME_TYPE_LEN			31
#define	MIME_SUBTYPE_LEN		31
#define	MIME_NAME_LEN			(3 * XPL_MAX_PATH + 20)	/* rfc2231 encoding can cause paths to be more than 3 times max path */ 
#define	MIME_SEPARATOR_LEN	127
#define	MIME_CHARSET_LEN		63
#define	MIME_ENCODING_LEN		64

typedef struct _MIMEPartStruct {
    unsigned char separator[MIME_SEPARATOR_LEN+1];
    unsigned char type[MIME_TYPE_LEN+1];
    unsigned char subtype[MIME_SUBTYPE_LEN+1];
    unsigned char charset[MIME_CHARSET_LEN+1];
    unsigned char encoding[MIME_ENCODING_LEN+1];
    unsigned char name[MIME_NAME_LEN+1];
    size_t headerStart;
    size_t headerLen;
    size_t start;
    size_t length;
    unsigned long lines;
    unsigned int sepLen;
    unsigned int end;
    BOOL multiMark;
    BOOL rfc822Start;
    BOOL rfc822Mark;
    int rfc822End;
} MIMEPartStruct;

typedef struct {
    unsigned long code;
    unsigned char *type;
    unsigned char *subtype;
    unsigned char *charset;
    unsigned char *encoding;
    unsigned char *name;
    unsigned long headerStart;
    unsigned long headerLen;
    unsigned long start;
    unsigned long len;
    unsigned long messageHeaderLen;
    unsigned long lines;
} MimeResponseLine;

typedef struct {
    char *base;
    unsigned long size;
    unsigned long partCount;
    unsigned long lineCount;
    MimeResponseLine *line;
} MimeReport;

MimeReport *MimeParse(FILE *fh, size_t messageSize, unsigned char *line, unsigned long lineSize);
BOOL MimeReportSend(void *param, MimeReport *report);
void MimeReportFixup(MimeReport *report);
void MimeReportFree(MimeReport *report);


XPL_END_C_LINKAGE

#endif
