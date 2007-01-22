/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2006 Novell, Inc. All Rights Reserved.
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

#ifndef _ALARM_H
#define _ALARM_H

#include <connio.h>
#include <mdb.h>
#include <management.h>
#include <msgapi.h>
#include <nmap.h>
#include <nmlib.h>
#include <bongoagent.h>

#include <libical/ical.h>

#define AGENT_NAME "bongoalarm"
#define AGENT_DN "Alarm Agent"


typedef struct {
    uint64_t guid;
    uint64_t when;
    char *summary;
    char *email;
    char *sms;
} AlarmInfo;

typedef struct {
    Connection *conn;
    char buffer[CONN_BUFSIZE + 1];
} StoreClient;

typedef struct {
    Connection *conn;
    char buffer[CONN_BUFSIZE + 1];
} QueueClient;

typedef struct _AlarmGlobals {
    BongoAgent agent;

    BongoArray storeclients;
    BongoArray alarms;

    /* Client management */
    Connection *clientListener;
    void *clientMemPool;
    BongoThreadPool *clientThreadPool;
} AlarmGlobals;

extern AlarmGlobals Alarm;

typedef enum {
    ALARM_COMMAND_NULL = 0,

    ALARM_COMMAND_PUT,
    ALARM_COMMAND_QUIT,

    ALARM_COMMAND_NOOP,
} AlarmCommand;

/* management.c */
int AlarmManagementStart(void);
void AlarmManagementShutdown(void);

#endif /* _ALARM_H */
