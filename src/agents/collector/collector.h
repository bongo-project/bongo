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

#ifndef _COLLECTOR_H
#define _COLLECTOR_H

#include <connio.h>
#include <msgapi.h>
#include <nmap.h>
#include <nmlib.h>
#include <bongoagent.h>

#include <libical/ical.h>

#define AGENT_NAME "bongocollector"
#define AGENT_DN "Collector Agent"

typedef struct {
    Connection *conn;

    char buffer[CONN_BUFSIZE + 1];
    char line[CONN_BUFSIZE + 1];
} CollectorClient;

typedef struct _CollectorGlobals {
    BongoAgent agent;

    /* Client management */
    Connection *clientListener;
    void *clientMemPool;
    BongoThreadPool *clientThreadPool;
} CollectorGlobals;

extern CollectorGlobals Collector;

typedef enum {
    COLLECTOR_COMMAND_NULL = 0,

    COLLECTOR_COMMAND_PUT,
    COLLECTOR_COMMAND_QUIT,

    COLLECTOR_COMMAND_NOOP,
} CollectorCommand;

CCode CollectorCommandPUT(CollectorClient *client, char *user, char *name, char *url);
CCode CollectorCommandQUIT(CollectorClient *client);

#endif /* _COLLECTOR_H */
