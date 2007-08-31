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

#ifndef _GENERIC_H
#define _GENERIC_H

#include <connio.h>
#include <msgapi.h>
#include <nmap.h>
#include <nmlib.h>
#include <bongoagent.h>

#define AGENT_NAME "bongogeneric"
#define AGENT_DN "GenericAgent"

typedef struct {
    Connection *conn;

    char *envelope;
    char line[CONN_BUFSIZE + 1];
} GAgentClient;

typedef struct _GAgentGlobals {
    BongoAgent agent;

    XplThreadID threadGroup;    

    unsigned long queueNumber;

    Connection *nmapConn;
    char nmapAddress[80];
    
    void *clientPool;
    BongoThreadPool *threadPool;
} GAgentGlobals;

extern GAgentGlobals GAgent;

#endif /* _GENERIC_H */
