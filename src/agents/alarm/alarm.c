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

#include <config.h>

#include <xpl.h>
#include <memmgr.h>
#include <logger.h>
#include <bongoutil.h>
#include <bongoagent.h>
#include <mdb.h>
#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include <connio.h>
#include "alarm.h"

#define TOK_ARR_SZ 10

AlarmGlobals Alarm = {{0,}, 0, };

static void
AlarmDestroy(AlarmInfo *alarm)
{
    if (alarm->summary) {
        MemFree(alarm->summary);
    }
    if (alarm->email) {
        MemFree(alarm->email);
    }
    if (alarm->sms) {
        MemFree(alarm->sms);
    }
}


static int
ConnectToStore(StoreClient *client, char *address)
{
    char buffer[CONN_BUFSIZE];

    client->conn = NMAPConnect(address, NULL);
    if (!client->conn) {
        printf(AGENT_NAME ": unable to connect to store %s.\r\n", address);
        return -1;
    }

    if (!NMAPAuthenticateToStore(client->conn, buffer, sizeof(buffer))) {
        NMAPQuit(client->conn);
        printf(AGENT_NAME ": authentication failed when connecting to store %s.\r\n", 
               address);
        return -1;
    }

    return 0;
}


static int
ConnectToQueue(QueueClient *client, char *address)
{
    char buffer[CONN_BUFSIZE];

    client->conn = NMAPConnectQueue(address, NULL);
    if (!client->conn) {
        printf(AGENT_NAME ": unable to connect to queue %s.\r\n", address);
        return -1;
    }

    if (!NMAPAuthenticate(client->conn, buffer, sizeof(buffer))) {
        NMAPQuit(client->conn);
        printf(AGENT_NAME ": authentication failed when connecting to queue %s.\r\n", 
               address);
        return -1;
    }

    return 0;
}


static int
GetAlarmProp(StoreClient *client, const char *propname, char **result)
{
    CCode ccode;
    int len;

    ccode = NMAPReadPropertyValueLength(client->conn, propname, &len);
    if (2001 == ccode) {
        *result= MemMalloc(len + 1);
        if (len != NMAPReadCount(client->conn, *result, len) ||
            2 != NMAPReadCrLf(client->conn)) 
        {
            MemFree(*result);
            return -1;
        }
        return 1;
    } else if (3245 == ccode) {
        return 0;
    } else {
        return -1;
    }
}


static int
GetAlarms(StoreClient *client, uint64_t start, uint64_t end)
{
    CCode ccode = 0;
    AlarmInfo *alarm = NULL;
    BongoArray alarms;

    if (BongoArrayInit(&alarms, sizeof (AlarmInfo *), 64)) {
        return -1;
    }

    ccode = NMAPSendCommandF(client->conn, "ALARMS %lld %lld\r\n", start, end);
    if (-1 == ccode) {
        return -1;
    }

    while (1) {
        ccode = NMAPReadResponse(client->conn, 
                                 client->buffer, sizeof(client->buffer), TRUE);
        switch (ccode) {
        case -1:
            return -1;
        case 1000:
            goto finish;
        case 2001:
            alarm = MemMalloc(sizeof(AlarmInfo));
            memset(alarm, 0, sizeof(AlarmInfo));

            if (1 != sscanf(client->buffer, "%*llx %lld", &alarm->when)) {
                printf (AGENT_NAME ": couldn't parse store response '%s'.\n", 
                        client->buffer);
                return -1;
            }

            if (1 != GetAlarmProp(client, "nmap.alarm.summary", &alarm->summary)) {
                goto abort;
            }
            ccode = GetAlarmProp(client, "nmap.alarm.email", &alarm->email);
            if (ccode < 0) {
                goto abort;
            }
            ccode = GetAlarmProp(client, "nmap.alarm.sms", &alarm->sms);
            if (ccode < 0) {
                goto abort;
            }

            BongoArrayAppendValue(&alarms, alarm);
            alarm = NULL;
        }
    }
     
finish:

    BongoArrayAppendValues(&Alarm.alarms, alarms.data, alarms.len);
    BongoArrayDestroy(&alarms, TRUE);

    return 0;

abort:
    if (alarm) {
        AlarmDestroy(alarm);
        MemFree(alarm);
    }
    return -1;

}


static int
SendAlarmEmail(QueueClient *qclient, AlarmInfo *alarm)
{
    int ccode;
    char buffer[4096];
    
    snprintf(buffer, sizeof(buffer),                  
             "Subject: %s\r\n"
             "From: %s\r\n"
             "To: %s\r\n"
             "\r\n"
             "%s\r\n"
             "(This message was automatically generated by the Bongo Alarm agent)\r\n"
             ,
             alarm->summary,
             alarm->email,
             alarm->email,
             alarm->summary);

    ccode = NMAPRunCommandF(qclient->conn, NULL, 0, "QCREA\r\n");
    if (1000 != ccode) {
        return -1;
    }
    ccode = NMAPRunCommandF(qclient->conn, NULL, 0, "QSTOR FROM %s %s\r\n",
                            alarm->email, alarm->email);
    if (1000 != ccode) {
        return -1;
    }

    ccode = NMAPRunCommandF(qclient->conn, NULL, 0, "QSTOR TO %s %s 0\r\n",
                            alarm->email, alarm->email);
    if (1000 != ccode) {
        return -1;
    }

    ccode = NMAPRunCommandF(qclient->conn, NULL, 0, "QSTOR MESSAGE %d\r\n%s",
                            strlen(buffer), buffer);
    if (1000 != ccode) {
        return -1;
    }

    ccode = NMAPRunCommandF(qclient->conn, NULL, 0, "QRUN\r\n");
    if (1000 != ccode) {
        return -1;
    }

    return 0;
}


static void 
AlarmLoop(void *ignored)
{
    MDBValueStruct *vs;
    int i;
    uint64_t start;
    uint64_t end;
    uint64_t now;
    uint64_t window = 5 * 60; /* five minutes */

    QueueClient *qclient;

    /* open store connections */

    {
        StoreClient *client;

        /* FIXME: should download a list of stores and connect to them all */

        client = MemMalloc(sizeof(StoreClient));
        if (!client) {
            printf(AGENT_NAME ": out of memory.\n");
            return;
        }

        if (!ConnectToStore(client, "127.0.0.1")) {
            BongoArrayAppendValue(&Alarm.storeclients, client);
        }       
    }

    /* open queue connection */

    qclient = MemMalloc(sizeof(QueueClient));
    if (!qclient) {
        printf(AGENT_NAME ": out of memory.\n");
        return;
    }
    if (ConnectToQueue(qclient, "127.0.0.1")) {
        return;
    }
    

    start = BongoCalTimeUtcAsUint64(BongoCalTimeNow(NULL));
    end = start + window;

    XplRenameThread(XplGetThreadID(), AGENT_DN);

    while (Alarm.agent.state < BONGO_AGENT_STATE_STOPPING) {
        if (Alarm.agent.state >= BONGO_AGENT_STATE_STOPPING) {
            break;
        }

        /* gather alarms from stores */
        
        for (i = 0; i < Alarm.storeclients.len; i++) {
            StoreClient *client;

            client = BongoArrayIndex(&Alarm.storeclients, StoreClient *, i);

            /* FIXME: */

            if (GetAlarms(client, start, end)) {
                /* FIXME: should disconnect client, re-connect, and try again */

                printf(AGENT_NAME ": Couldn't get alarms from store.\n");
            }
            
        }

        /* send alarms */
        /* FIXME: on failure, should retry or try another way to deliver the alarm */

        for (i = 0; i < Alarm.alarms.len; i++) {
            AlarmInfo *alarm;

            alarm = BongoArrayIndex(&Alarm.alarms, AlarmInfo *, i);
            
            if (alarm->email) {
                if (SendAlarmEmail(qclient, alarm)) {
                    printf("Error sending email alarm to %s: %s\n", 
                           alarm->email, alarm->summary);
                }
            } else if (alarm->sms) {
                printf("Not sending sms alarm to %s: %s\n", 
                       alarm->sms, alarm->summary);
            }

            AlarmDestroy(alarm);
            MemFree(alarm);

        }

        BongoArraySetLength(&Alarm.alarms, 0);

        now = BongoCalTimeUtcAsUint64(BongoCalTimeNow(NULL));

        if (now >= end) {
            /* we're behind, catch up */
            start = end;
            end = now + window * 2;
            if (start < now - 4 * window) {
                /* don't bother send warnings that are way too late */
                start = now - 4 * window;
            }
        } else {
            sleep(end - now);
            start += window;
            end += window;
        }
    }

    /* Shutting down */
    XplConsolePrintf(AGENT_NAME ": Shutting down.\r\n");
}


static BOOL 
ReadConfiguration(void)
{
    MDBValueStruct *config;

    config = MDBCreateValueStruct(Alarm.agent.directoryHandle, 
                                  MsgGetServerDN(NULL));
    if (config) {
        /* Read your agent's configuration here */
    } else {
        return FALSE;
    }

    MDBDestroyValueStruct(config);

    return TRUE;
}


static void 
SignalHandler(int sigtype) 
{
    BongoAgentHandleSignal(&Alarm.agent, sigtype);
}


XplServiceCode(SignalHandler)


int
XplServiceMain(int argc, char *argv[])
{
    int ccode;
    int startupOpts;

    if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf(AGENT_NAME ": Could not drop to unprivileged user '%s'\r\n" AGENT_NAME ": exiting.\n", MsgGetUnprivilegedUser());
        return -1;
    }
    XplInit();

    /* Initialize the Bongo libraries */
    startupOpts = BA_STARTUP_MDB | BA_STARTUP_CONNIO;
    ccode = BongoAgentInit(&Alarm.agent, AGENT_NAME, AGENT_DN, DEFAULT_CONNECTION_TIMEOUT, startupOpts);
    if (ccode == -1) {
        XplConsolePrintf(AGENT_NAME ": Exiting.\r\n");
        return -1;
    }

    ReadConfiguration();

    XplSignalHandler(SignalHandler);

    StreamioInit();

    /* setup globals */

    if (BongoArrayInit(&Alarm.alarms, sizeof (AlarmInfo *), 256)) {
        printf(AGENT_NAME ": out of memory.\n");
        return -1;
    }

    if (BongoArrayInit(&Alarm.storeclients, sizeof (StoreClient), 8)) {
        printf(AGENT_NAME ": out of memory.\n");
        return -1;
    }

    /* Start the alarm thread */
    XplStartMainThread(AGENT_NAME, &colid, AlarmLoop, 8192, NULL, ccode);
    
    XplUnloadApp(XplGetThreadID());
    
    return 0;
}
