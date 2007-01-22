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

#include <bongostream.h>

#define SEARCH_MAX_UTF8_STRING 1024
#define SEARCH_MAX_DATE_STRING 512
#define SEARCH_MAX_CHARSET 20

typedef struct _SearchKey {
    long *matchList;
    unsigned long messageCount;
    char charset[SEARCH_MAX_CHARSET];
    BongoStream *stream;
    BOOL matchDropped;
} SearchKey;

typedef long (* SearchKeyHandler)(ImapSession *session, char **keyString, SearchKey *key);

typedef struct {
    char *name;
    unsigned long nameLen;
    SearchKeyHandler handler;
} SearchKeyInfo;


