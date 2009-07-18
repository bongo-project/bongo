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

#define F_UID (1<<0)
#define F_FLAGS (1<<1)
#define F_ENVELOPE (1<<2)
#define F_XSENDER (1<<3)
#define F_INTERNALDATE (1<<4)
#define F_BODYSTRUCTURE (1<<5)
#define F_RFC822_TEXT (1<<6)
#define F_RFC822_SIZE (1<<7)
#define F_RFC822_HEADER (1<<8)
#define F_RFC822_BOTH (1<<9)
#define F_BODY (1<<10)
#define F_BODY_HEADER (1<<11)
#define F_BODY_HEADER_PARTIAL (1<<12)
#define F_BODY_TEXT (1<<13)
#define F_BODY_TEXT_PARTIAL (1<<14)
#define F_BODY_BOTH (1<<15)
#define F_BODY_BOTH_PARTIAL (1<<16)
#define F_BODY_MIME (1<<17)
#define F_BODY_MIME_PARTIAL (1<<18)
#define F_BODY_HEADER_FIELDS (1<<19)
#define F_BODY_HEADER_FIELDS_NOT (1<<20)
#define F_BODY_PART (1<<21)
#define F_BODY_SEEN (1<<22) 

#define F_PARSE_ERROR (1<<30)
#define F_SYSTEM_ERROR (1<<31)

#define F_NEED_HEADER (F_BODY_HEADER | F_BODY_HEADER_PARTIAL | F_RFC822_HEADER | F_RFC822_BOTH | F_ENVELOPE | F_BODY_HEADER_FIELDS | F_BODY_HEADER_FIELDS_NOT | F_BODY_MIME | F_BODY_MIME_PARTIAL)
#define F_NEED_MIME (F_BODY_PART | F_BODYSTRUCTURE | F_BODY)

#define F_ERROR (F_PARSE_ERROR | F_SYSTEM_ERROR)

#define ALLOCATED_FLAGS 1 << 0
#define ALLOCATED_PART_NUMBERS 1 << 1
#define ALLOCATED_FIELD_POINTERS 1 << 2
#define ALLOCATED_FIELDS 1 << 3


typedef struct {
    unsigned long start;
    unsigned long len;
} PartialRequestStruct;

#define FETCH_FIELD_ALLOC_THRESHOLD 3

typedef struct {
    unsigned long count;
    unsigned char **field;
    unsigned char *fieldEmbedded[FETCH_FIELD_ALLOC_THRESHOLD];
    unsigned char *fieldRequest;
    BOOL skip;
} HeaderFieldStruct;

typedef long (* FetchResponder)(void *param1, void *param2, void *param3);

#define PART_DEPTH_ALLOC_THRESHOLD 5

typedef struct {
    unsigned long depth;
    unsigned long *part;
    unsigned long partEmbedded[PART_DEPTH_ALLOC_THRESHOLD];
    long mimeOffset;
    unsigned long mimeLen;
    unsigned long partOffset;
    unsigned long partLen;
    BOOL isMessage;
    unsigned long messageHeaderLen;
    FetchResponder responder;
} BodyPartRequestStruct;

typedef struct {
    unsigned long sequenceNumber;
    unsigned char *header;
    GArray *mimeInfo;
} MessageDetail;

typedef struct {
    FetchResponder responder;

    unsigned long hasAllocated;
    BOOL isPartial;
    PartialRequestStruct partial;
    HeaderFieldStruct fieldList;
    BodyPartRequestStruct bodyPart;
} FetchFlagStruct;

#define FETCH_FLAG_ALLOC_THRESHOLD 12

typedef struct {
    unsigned long flags;

    unsigned char *messageSet;

    unsigned long flagCount;
    FetchFlagStruct *flag;
    FetchFlagStruct flagEmbedded[FETCH_FLAG_ALLOC_THRESHOLD];
    unsigned long hasAllocated;

    MessageInformation *message;
    MessageDetail messageDetail;
} FetchStruct;

typedef unsigned long (* FetchFlagParser)(unsigned char **ptr, FetchFlagStruct *flag);

typedef struct {
    unsigned char *name;
    unsigned long nameLen;
    unsigned long value;
    FetchFlagParser parser;
    FetchResponder responder;
} FetchFlag;
