/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2005-6 Novell, Inc. All Rights Reserved.
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

#ifndef MAIL_PARSER_H
#define MAIL_PARSER_H
    
#include "stored.h"

XPL_BEGIN_C_LINKAGE

#define XPL_MAX_RFC822_LINE 1000

typedef enum {
    MAIL_OPTIONAL = 0,

    MAIL_FROM     = 1 << 1,
    MAIL_SENDER   = 1 << 2,
    MAIL_REPLY_TO = 1 << 3,
    MAIL_TO       = 1 << 4,
    MAIL_CC       = 1 << 5,
    MAIL_BCC      = 1 << 6,

    MAIL_DATE     = 1 << 7,
    MAIL_SUBJECT  = 1 << 8,
    MAIL_SPAM_FLAG = 1 << 9,

    MAIL_MESSAGE_ID = 1 << 10,
    MAIL_IN_REPLY_TO = 1 << 11,
    MAIL_REFERENCES = 1 << 12,
    MAIL_OFFSET_BODY_START = 1 << 13,
    MAIL_X_AUTH_OK = 1 << 14,

    MAIL_X_BEEN_THERE = 1 << 15,
    MAIL_X_LOOP = 1 << 16,
    MAIL_LIST_ID = 1 << 17,
} MailHeaderName;

typedef struct MailAddress {
    char *displayName;
    char *address;
/*    struct MailAddress *next; */
} MailAddress;

typedef struct MailParser MailParser;

typedef void (*MailParserParticipantsCallback)(void *cbdata, MailHeaderName name, MailAddress *address);
typedef void (*MailParserUnstructuredCallback)(void *cbdata, MailHeaderName name, const char *nameRaw, const char *data);
typedef void (*MailParserMessageIdCallback)(void *cbdata, MailHeaderName name, const char *data);
typedef void (*MailParserDateCallback)(void *cbdata, MailHeaderName name, uint64_t date);
typedef void (*MailParserOffsetCallback)(void *cbdata, MailHeaderName name, uint64_t offset);

MailParser *MailParserInit(FILE *stream, void *cbData);
void MailParserDestroy(MailParser *p);

void MailParserSetParticipantsCallback(MailParser *p, 
                                       MailParserParticipantsCallback cbfunc);

void MailParserSetUnstructuredCallback(MailParser *p,
                                       MailParserUnstructuredCallback cbfunc);
void MailParserSetDateCallback(MailParser *p,
                               MailParserDateCallback cbfunc);
void MailParserSetOffsetCallback(MailParser *p,
                               MailParserOffsetCallback cbfunc);
void MailParserSetMessageIdCallback(MailParser *p,
                                    MailParserMessageIdCallback cbfunc);

int MailParseHeaders(MailParser *parser);
int MailParseBody(MailParser *parser);

XPL_END_C_LINKAGE

#endif
