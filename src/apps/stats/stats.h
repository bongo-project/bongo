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

#ifndef _BONGO_STATISTICS
#define _BONGO_STATISTICS

typedef enum _BongoStatsStates {
    BONGOSTATS_STATE_STARTING = 0, 
    BONGOSTATS_STATE_INITIALIZING, 
    BONGOSTATS_STATE_LOADING, 
    BONGOSTATS_STATE_RUNNING, 
    BONGOSTATS_STATE_RELOADING, 
    BONGOSTATS_STATE_UNLOADING, 
    BONGOSTATS_STATE_STOPPING, 
    BONGOSTATS_STATE_DONE, 

    BONGOSTATS_STATE_MAX_STATES
} HSStates;

typedef struct _BongoStatsGlobals {
    HSStates state;

    unsigned char serverDN[MDB_MAX_OBJECT_CHARS + 1];

    time_t start;

    MDBHandle directory;

    struct {
        XplThreadID main;
        XplThreadID group;
    } id;

    struct {
        Connection *conn;

        unsigned char dn[MDB_MAX_OBJECT_CHARS + 1];
        unsigned char rdn[MDB_MAX_OBJECT_CHARS + 1];
        unsigned char line[CONN_BUFSIZE + 1];
    } selected;

    struct {
        XplSemaphore main;
        XplSemaphore shutdown;
    } sem;

    struct {
        StatisticsStruct cleared;
        StatisticsStruct current;
    } server;

    struct {
        BOOL enable;

        ConnSSLConfiguration config;

        bongo_ssl_context *context;

        Connection *conn;
    } ssl;

    struct {
        AntispamStatisticsStruct cleared;
        AntispamStatisticsStruct current;
    } spam;
} HSGlobals;

/* bongostats.c */
extern HSGlobals BongoStats;

int BongoStatsInitialize(int argc, char *argv[]);

void StatPrintNumber(unsigned long number, unsigned char *buffer);
void StatPrintFloat(unsigned long number, unsigned long divideBy, unsigned char *buffer);
void StatPrintTime(unsigned long seconds, unsigned char *buffer);

int ReleaseMessagingServer(void);
int ConnectToMessagingServer(unsigned char *dn);
int LoadStatistics(StatisticsStruct *stats);
int LoadSpamStatistics(AntispamStatisticsStruct *spamStats);

/* platform specific */
int BongoStatsUIStart(void);
int BongoStatsUIRun(void);
void BongoStatsUIStop(void);

#endif /* _BONGO_STATISTICS */
