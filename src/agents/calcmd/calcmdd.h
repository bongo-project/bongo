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

#ifndef _CALCMDD_H
#define _CALCMDD_H

#include <connio.h>
#include <msgapi.h>
#include <nmap.h>
#include <nmlib.h>
#include <bongoagent.h>
#include <bongoutil.h>

#define AGENT_NAME "bongocalcmd"
#define AGENT_DN MSGSRV_AGENT_CALCMD

typedef struct {
    Connection *conn;

    char *envelope;
    char line[CONN_BUFSIZE + 1];
    
    char from[CONN_BUFSIZE];
    char recipient[CONN_BUFSIZE];

    char *cmd;
    BongoSList *users;
} CalCmdClient;

typedef struct _CalCmdAgentGlobals {
    BongoAgent agent;

    XplThreadID threadGroup;    

    unsigned long queueNumber;

    Connection *nmapConn;
    char nmapAddress[80];
    
    void *clientPool;
    BongoThreadPool *threadPool;
} CalCmdAgentGlobals;

extern CalCmdAgentGlobals CalCmdAgent;

#endif /* _GENERIC_H */
