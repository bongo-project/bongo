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

#ifndef _CONF_H
#define _CONF_H

#include <xpl.h>
#include <connio.h>
#include <management.h>
#include <msgapi.h>
#include <nmap.h>
#include <nmlib.h>
#include <bongoagent.h>
#include <bongoutil.h>

typedef enum {
    BOUNCE_RETURN = (1 << 0),
    BOUNCE_CC_POSTMASTER = (1 << 1)
} BounceFlags;

typedef struct _QueueConfiguration {
    BOOL debug;
    
    XplRWLock lock;

    /* Server info */
    char hostname[MAXEMAILNAMESIZE + 1];    
    char serverHash[NMAP_HASH_SIZE];
    char postMaster[MAXEMAILNAMESIZE + 1];
    char officialName[MAXEMAILNAMESIZE + 1];

    /* Quotas */
    char *quotaMessage;

    /* Trusted hosts */
    BongoArray *trustedHosts;

    /* Paths */
    char spoolPath[XPL_MAX_PATH + 1];
    char queueClientsPath[XPL_MAX_PATH + 1];    

    /* Deferral */
    BOOL deferEnabled;
    char i_deferStartWD;
    char i_deferStartWE;
    char i_deferEndWD;
    char i_deferEndWE;
    char deferStart[7];
    char deferEnd[7];

    time_t maxLinger;

    time_t queueInterval;

    /* Forward Undeliverable */
    BOOL forwardUndeliverableEnabled;
    char forwardUndeliverableAddress[MAXEMAILNAMESIZE + 1];

    /* Queue tuning */
    long defaultConcurrentWorkers;
    long maxConcurrentWorkers;
    long defaultSequentialWorkers;
    long maxSequentialWorkers;
    long queueCheck;
    long limitTrigger;

    unsigned long loadMonitorLow;
    unsigned long loadMonitorHigh;

    /* Monitors */
    BOOL loadMonitorDisabled;

    /* Diskspace */
    unsigned long minimumFree;

    /* Bounce configuration */
    BOOL bounceBlockSpam;
    time_t bounceInterval;
    unsigned long bounceMaxBodySize;
    unsigned long bounceMax;

    BOOL b_bounceReturn;
    BOOL b_bounceCCPostmaster;
    BounceFlags bounceHandling;

    /* Bookkeeping */
    unsigned long lastRead;
    
    /* aliasing system */
    BongoArray *hostedDomains;
    BongoArray *aliasList;
} QueueConfiguration;

typedef struct _AliasStruct{
    unsigned char* original;
    unsigned char* to;
    BongoArray *aliases;
} AliasStruct;

extern QueueConfiguration Conf;

void CheckConfig(BongoAgent *agent);

BOOL ReadConfiguration(BOOL *recover);

long CalculateCheckQueueLimit(unsigned long concurrent, unsigned long sequential);

#endif /* _DELIVERYQUEUE_H */
