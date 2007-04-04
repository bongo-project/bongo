/****************************************************************************
 *
 * Copyright (c) 2001-2006 Novell, Inc.
 * All Rights Reserved.
 *
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2.1 of the GNU Lesser General Public
 * License as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
 * GNU Lesser General Public License for more details.
 *
 * You should have received a copy of the GNU Lesser General Public License
 * along with this program; if not, contact Novell, Inc.
 *
 * To contact Novell about this file by physical or electronic mail,
 * you may find current contact information at www.novell.com
 *
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
#include <calcmd.h>

#include "calcmdd.h"

CalCmdAgentGlobals CalCmdAgent = {{0,}, 0, };

static void 
CalCmdClientFree(void *clientp)
{
    CalCmdClient *client = clientp;

    if (client->envelope) {
        MemFree(client->envelope);
    }

    if (client->cmd) {
        MemFree(client->cmd);
    }

    MemPrivatePoolReturnEntry(client);
}

static char *
GetCommand(CalCmdClient *client, const char *qID)
{

    NmapMimeInfo info = {{0}, };

    if (!NMAPQueueFindToplevelMimePart(client->conn, qID, 
                                       "text", "plain", &info)) {
        printf("didn't find mime part\n");
        return NULL;
    }

    return NMAPQueueReadMimePartUTF8(client->conn, qID, &info, 1024);
}

static BOOL
CheckCode(const char *user, const char *code)
{
    /* FIXME: until we have a form to change your suffix, everyone
     * will default to 123 */
    if (!strcmp(code, "123")) {
        return TRUE;
    } else {
        return FALSE;
    }
}

static BOOL 
GetRecipient(char *fromLine, const char **userOut, const char **codeOut)
{
    char *delim;
    char *code;
    
    /* Looking for a local delivery to address of form
     * "user+code" */
    delim = strchr(fromLine, ' ');
    if (delim) {
        *delim = '\0';
    }
    
    /* BIG FIXME: need to handle + addresses more globally,
     * now that I think about it */ 
    delim = strchr(fromLine, '@');
    if (delim) {
        *delim = '\0';
    }
    
    delim = strchr(fromLine, '+');
    if (delim) {
        *delim = '\0';
        code = delim + 1;
    } else {
        return FALSE;
    }

    *userOut = fromLine;
    *codeOut = code;

    return TRUE;
}

static BongoCalTimezone * 
GetDefaultTimezone(CalCmdClient *client, Connection *conn)
{
    /* FIXME: read from the user's preferences */
    return BongoCalTimezoneGetSystem("/bongo/America/New_York");
}


static BOOL
ReadEnvelope(CalCmdClient *client, char *envelope, 
             char *sender, int senderLen, 
             char *recipient, int recipientLen)
{
    char *ptr;
    char *delim;
    const char *user;
    const char *code;
    
    sender[0] = '\0';
    recipient[0] = '\0';
    
    ptr = client->envelope;
    while (*ptr) {
        switch(*ptr++) {
        case QUEUE_FROM :
            delim = strchr(ptr, ' ');
            if (delim) {
                *delim = '\0';
            }
            BongoStrNCpy(sender, ptr, senderLen);
        case QUEUE_RECIP_LOCAL :
        case QUEUE_RECIP_MBOX_LOCAL :
            if (!GetRecipient(ptr, &user, &code)) {
                break;
            }

            printf("checking %s %s\n", user, code);
            
            if (CheckCode(user, code)) {
                BongoStrNCpy(recipient, user, recipientLen);
            }

            break;
        }
        BONGO_ENVELOPE_NEXT(ptr);
    }

    return sender[0] != '\0' && recipient[0] != '\0';
}

static void
PrintOccurrences(CalCmdClient *client, 
                 BongoArray *occurrences, 
                 BongoStringBuilder *message)
{
    unsigned int i;
    
    if (occurrences->len == 0) {
        BongoStringBuilderAppend(message, "No events.\r\n");
        return;
    }

    for (i = 0; i < occurrences->len; i++) {
        BongoCalOccurrence occ = BongoArrayIndex(occurrences, BongoCalOccurrence, i);
        const char *summary;
        
        BongoCalTimeToStringConcise(occ.start, 
                                   client->line,
                                   CONN_BUFSIZE);

        BongoStringBuilderAppend(message, client->line);
        summary = BongoCalInstanceGetSummary(occ.instance);
        if (summary) {
            BongoStringBuilderAppendF(message, ": %s", summary);
        }
        BongoStringBuilderAppend(message, "\r\n");
    }
}

static BOOL
RunQuery(CalCmdClient *client,
         Connection *conn,
         CalCmdCommand *calcmd,
         BongoStringBuilder *message,
         const char *eventsMessage,
         const char *noEventsMessage)
{
    BongoCalObject *cal;
    BongoArray *occurrences;
    
    cal = NMAPGetEvents(conn, NULL, calcmd->begin, calcmd->end);    
    
    if (!cal) {
        printf("no events!\n");        
        if (noEventsMessage) {
            BongoStringBuilderAppend(message, noEventsMessage);
        }
        return TRUE;
    }

    occurrences = BongoArrayNew(sizeof(BongoCalOccurrence), 16);
    if (!BongoCalObjectCollect(cal, calcmd->begin, calcmd->end, NULL, TRUE, occurrences)) {
        BongoCalObjectFree(cal, TRUE);
        if (noEventsMessage) {
            BongoStringBuilderAppend(message, noEventsMessage);
        }
        return TRUE;
    }
    
    if (eventsMessage) {
        BongoStringBuilderAppend(message, eventsMessage);
    }
    
    BongoCalOccurrencesSort(occurrences);
    PrintOccurrences(client, occurrences, message);

    BongoArrayFree(occurrences, TRUE);
    BongoCalObjectFree(cal, TRUE);


    return TRUE;
}   

static BOOL
AddEvent(CalCmdClient *client, Connection *conn, CalCmdCommand *cmd, const char *calendar)
{
    BOOL ret;
    BongoCalObject *cal;

    MsgGetUid(client->line, CONN_BUFSIZE);
    
    cal = CalCmdProcessNewAppt(cmd, client->line);

    printf("adding %s\n", BongoJsonObjectToString(BongoCalObjectGetJson(cal)));

    if (!cal) {
        return FALSE;
    }

    ret = NMAPAddEvent(conn, cal, calendar, NULL, 0);
    
    return ret;
}

static void
SendResponse(CalCmdClient *client,
             BongoStringBuilder *message)
{
    int ccode;
    
    ccode = NMAPRunCommandF(client->conn, client->line, CONN_BUFSIZE,
                            "QCREA 0\r\n");
    if (ccode != 1000) {
        return;
    }
 
    ccode = NMAPRunCommandF(client->conn, client->line, CONN_BUFSIZE,
                            "QSTOR FROM %s@%s %s@%s\r\n",
                            client->recipient, CalCmdAgent.agent.officialName,
                            client->recipient, CalCmdAgent.agent.officialName);
    if (ccode != 1000) {
        return;
    }
    
    ccode = NMAPRunCommandF(client->conn, client->line, CONN_BUFSIZE,
                            "QSTOR TO %s %s 0\r\n",
                            client->from, client->from);
    if (ccode != 1000) {
        return;
    }
    
    ccode = NMAPSendCommandF(client->conn, "QSTOR MESSAGE %d\r\n",
                             message->len);
    if (ccode < 0) {
        return;
    }
    
    NMAPWrite(client->conn, message->value, message->len);
    
    ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE);
    if (ccode != 1000) {
        return;
    }
    
    NMAPRunCommandF(client->conn, client->line, CONN_BUFSIZE, 
                    "QRUN\r\n");
}

static void
StartMessage(CalCmdClient *client, BongoStringBuilder *sb, const char *subject)
{
    BongoStringBuilderAppendF(sb,
                             "From: %s\r\n"
                             "Subject: %s\r\n"
                             "To: %s\r\n\r\n",
                             client->recipient,
                             subject,
                             client->from);
}




static BOOL
RunCommand(CalCmdClient *client)
{
    CalCmdCommand calcmd;
    BongoStringBuilder sb;
    BongoCalTimezone *tz;
    Connection *conn;

    BongoStringBuilderInit(&sb);

    conn = MsgNmapConnectEx(client->recipient, client->recipient, 
                            NULL, client->conn->trace.destination);
    if (!conn) {
        StartMessage(client, &sb, "cal error");
        BongoStringBuilderAppend(&sb, "Server error, unable to run command.\r\n");
        goto done;
    }

    tz = GetDefaultTimezone(client, conn);

    if (!CalCmdParseCommand(client->cmd, &calcmd, tz)) {
        calcmd.type = CALCMD_ERROR;
    }
    
    switch (calcmd.type) {
    case CALCMD_QUERY_CONFLICTS:
    case CALCMD_QUERY :
        StartMessage(client, &sb, "results");
        RunQuery(client, conn, &calcmd, &sb, NULL, "No events.\r\n");
        break;
    case CALCMD_NEW_APPT :
        RunQuery(client, conn, &calcmd, &sb, "Conflicts:\r\n", NULL);
        
        if (AddEvent(client, conn, &calcmd, "Personal")) {
            StartMessage(client, &sb, "cal success");
            calcmd.data[90] = '\0';
            BongoStringBuilderAppendF(&sb,
                                     "OK, new appt: %s ", calcmd.data);
            
            BongoCalTimeToStringConcise(calcmd.begin, client->line, CONN_BUFSIZE);
            BongoStringBuilderAppendF(&sb, "%s\r\n", client->line);
        } else {
            StartMessage(client, &sb, "cal error");
            BongoStringBuilderAppend(&sb, "Unable to add event.\r\n");
        }
        break;
    case CALCMD_ERROR :
        StartMessage(client, &sb, "cal error");
        
        BongoStringBuilderAppend(&sb, 
                                "Unable to process your message.\r\n"
                                "For help on Bongo messages, send 'help'.\r\n");
        break;
    case CALCMD_HELP :
        StartMessage(client, &sb, "cal help");
        BongoStringBuilderAppend(&sb, 
                                "Send date/time to see your schedule, "
                                "e.g. 'today' or 'fri nite'.\r\n"
                                "Send date/time and summary to add a new "
                                "item, e.g. 'meeting 3pm' or "
                                "'Oct 15 payday'.\r\n");
        break;
    }

done :

    SendResponse(client, &sb);

    BongoStringBuilderDestroy(&sb);

    if (conn) {
        NMAPQuit(conn);
        ConnFree(conn);
    }

    return TRUE;
}

static int 
ProcessEntry(void *clientp,
             Connection *conn)
{
    int ccode;
    int envelopeLength;
    CalCmdClient *client = clientp;
    char qID[16];

    client->conn = conn;

    ccode = BongoQueueAgentHandshake(client->conn, 
                                    client->line, 
                                    qID, 
                                    &envelopeLength);

    sprintf(client->line, "CalCmdAgent: %s", qID);
    XplRenameThread(XplGetThreadID(), client->line);

    if (ccode == -1) {
        return -1;
    }

    client->envelope = BongoQueueAgentReadEnvelope(client->conn, 
                                                  client->line, 
                                                  envelopeLength);

    if (client->envelope == NULL) {
        return -1;
    }

    if (!ReadEnvelope(client, client->envelope, 
                      client->from, CONN_BUFSIZE,
                      client->recipient, CONN_BUFSIZE)) {
        return -1;
    }

    printf("getting command for %s\n", client->recipient);

    client->cmd = GetCommand(client, qID);
    if (!client->cmd) {
        printf("no command\n");
        return -1;        
    }

    printf("running command\n");

    RunCommand(client);
    
    /* The client struct, connection, and QDONE will be cleaned up by
     * the caller */
    return 0;
}

static void 
CalCmdAgentServer(void *ignored)
{
    XplRenameThread(XplGetThreadID(), AGENT_DN " Server");

    /* Listen for incoming queue items.  Call ProcessEntry with a
     * CalCmdClient allocated for each incoming queue entry. */
    BongoQueueAgentListenWithClientPool(&CalCmdAgent.agent,
                                       CalCmdAgent.nmapConn,
                                       CalCmdAgent.threadPool,
                                       sizeof(CalCmdClient),
                                       CalCmdClientFree, 
                                       ProcessEntry,
                                       &CalCmdAgent.clientPool);

    /* Shutting down */
    XplConsolePrintf(AGENT_NAME ": Shutting down.\r\n");

    if (CalCmdAgent.nmapConn) {
        ConnClose(CalCmdAgent.nmapConn, 1);
        CalCmdAgent.nmapConn = NULL;
    }

    BongoThreadPoolShutdown(CalCmdAgent.threadPool);
    BongoAgentShutdown(&CalCmdAgent.agent);

    XplConsolePrintf(AGENT_NAME ": Shutdown complete\r\n");
}

static BOOL 
ReadConfiguration(void)
{
    MDBValueStruct *config;

    config = MDBCreateValueStruct(CalCmdAgent.agent.directoryHandle, 
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
    BongoAgentHandleSignal(&CalCmdAgent.agent, sigtype);
}

XplServiceCode(SignalHandler)

int
XplServiceMain(int argc, char *argv[])
{
    int ccode;
    int minThreads;
    int maxThreads;
    int minSleep;

    if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf(AGENT_NAME ": Could not drop to unprivileged user '%s'\r\n" AGENT_NAME ": exiting.\n", MsgGetUnprivilegedUser());
        return -1;
    }
    XplInit();

    strcpy(CalCmdAgent.nmapAddress, "127.0.0.1");

    /* Initialize the Bongo libraries */
    ccode = BongoAgentInit(&CalCmdAgent.agent, AGENT_NAME, AGENT_DN, DEFAULT_CONNECTION_TIMEOUT);
    if (ccode == -1) {
        XplConsolePrintf(AGENT_NAME ": Exiting.\r\n");
        return -1;
    }

    BongoCalInit(MsgGetDBFDir(NULL));

    /* Set up socket for listening on an incoming queue */ 
    CalCmdAgent.queueNumber = Q_FIVE;
    CalCmdAgent.nmapConn = BongoQueueConnectionInit(&CalCmdAgent.agent, CalCmdAgent.queueNumber);

    if (!CalCmdAgent.nmapConn) {
        BongoAgentShutdown(&CalCmdAgent.agent);
        XplConsolePrintf(AGENT_NAME ": Exiting.\r\n");
        return -1;
    }

    BongoQueueAgentGetThreadPoolParameters(&CalCmdAgent.agent,
                                          &minThreads, &maxThreads, &minSleep);
    
    /* Create a thread pool for managing connections */
    CalCmdAgent.threadPool = BongoThreadPoolNew(AGENT_DN " Clients",
                                          BONGO_QUEUE_AGENT_DEFAULT_STACK_SIZE,
                                          minThreads, maxThreads, minSleep);

    if (CalCmdAgent.threadPool == NULL) {
        BongoAgentShutdown(&CalCmdAgent.agent);
        XplConsolePrintf(AGENT_NAME ": Unable to create thread pool.\r\n" AGENT_NAME ": Exiting.\r\n");
        return -1;
    }
    
    ReadConfiguration();

    XplSignalHandler(SignalHandler);

    /* Start the server thread */
    XplStartMainThread(AGENT_NAME, &id, CalCmdAgentServer, 8192, NULL, ccode);
    
    XplUnloadApp(XplGetThreadID());
    
    return 0;
}
