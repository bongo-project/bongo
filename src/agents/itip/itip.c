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
#include <bongostream.h>
#include <bongocal.h>
#include <bongostore.h>

#include "itip.h"

ItipAgentGlobals ItipAgent = {{0,}, 0, };

typedef enum {
    INVITATION_READ = (1 << 0),
    INVITATION_WRITE = (1 << 1),
} InvitationRights;

static void 
ItipAgentClientFree(void *clientp)
{
    ItipAgentClient *client = clientp;

    if (client->envelope) {
        MemFree(client->envelope);
    }

    MemPrivatePoolReturnEntry(client);
}

static BOOL
FindObjects(Connection *conn, 
            const char *id, 
            NmapMimeInfo *calInfo,
            BOOL *foundCal,
            NmapMimeInfo *invitationInfo,
            BOOL *foundInvitation)
{
    NmapMimeInfo info = { {0}, };
    int ccode;
    int depth = 0;
    int alternative = 0;
    char buffer[CONN_BUFSIZE];

    *foundCal = *foundInvitation = FALSE;
    
    ccode = NMAPRunCommandF(conn, buffer, CONN_BUFSIZE, "QMIME %s\r\n", id);
    do {
        switch (ccode) {
        case 2002 :
            if (NMAPParseMIMELine(buffer, &info)) {
                if (!XplStrCaseCmp(info.type, "multipart") || !XplStrCaseCmp(info.type, "message")) {
                    depth++;
                    if (!XplStrCaseCmp(info.subtype, "alternative")) {
                        alternative++;
                    }
                } else if ((depth < 2
                            || (depth < 3 && alternative == 1))) {
                    if (!XplStrCaseCmp(info.type, "text")
                        && !XplStrCaseCmp(info.subtype, "calendar")) {
                        *calInfo = info;
                        *foundCal = TRUE;
                    } else if (!XplStrCaseCmp(info.type, "text") 
                               && !XplStrCaseCmp(info.subtype, "x-bongo-invitation")) {
                        *invitationInfo = info;
                        *foundInvitation = TRUE;
                    }
                }
            }
            break;
        case 2004 :
            alternative--;
            /* Fall through */
        case 2003 :
            depth--;
            break;
        default :
            break;
        }
        ccode = NMAPReadAnswer(conn, buffer, CONN_BUFSIZE, TRUE);
    } while (ccode >= 2002 && ccode <= 2004);

    return (ccode == 1000);
}

static BOOL
GetObjects(ItipAgentClient *client, const char *qID, 
           BongoCalObject **calOut, char **invitationOut)
{
    NmapMimeInfo calInfo = {{0}, };
    BOOL haveCal;
    NmapMimeInfo invitationInfo = {{0}, };
    BOOL haveInvitation;
    char *data;

    *calOut = NULL;
    *invitationOut = NULL;

    if (!FindObjects(client->conn, qID, 
                     &calInfo, &haveCal,
                     &invitationInfo, &haveInvitation)) {
        return FALSE;
    }

    if (!haveCal && !haveInvitation) {
        return FALSE;
    }

    if (haveCal) {
        data = NMAPQueueReadMimePartUTF8(client->conn, qID, &calInfo, -1);
        if (!data) {
            return FALSE;
        }

        *calOut = BongoCalObjectParseIcalString(data);

        MemFree(data);
    }

    if (haveInvitation) {
        data = NMAPQueueReadMimePartUTF8(client->conn, qID, &invitationInfo, -1);
        if (!data) {
            goto error;
        }

        if (BongoJsonValidateString(data) != BONGO_JSON_OK) {
            MemFree(data);
            goto error;
        }

        *invitationOut = data;
    }

    return TRUE;
error:
            
    if (*calOut) {
        BongoCalObjectFree(*calOut, TRUE);
        *calOut = NULL;
    }

    return FALSE;
}

static BOOL
ApplyCalObject(ItipAgentClient *client,
               Connection *nmap, 
               BongoCalObject *cal)
{
    switch(BongoCalObjectGetMethod(cal)) {
    case BONGO_CAL_METHOD_PUBLISH:
    case BONGO_CAL_METHOD_REQUEST:
    case BONGO_CAL_METHOD_NONE:
        /* Put in the user's Incoming calendar */
        BongoCalObjectSetMethod(cal, BONGO_CAL_METHOD_NONE);

        return NMAPAddEvent(nmap, cal, "Invitations", NULL, 0);
    default :
        /* FIXME */
        break;
    }

    return TRUE;
}

static BOOL 
SaveCalendarInvitation(ItipAgentClient *client,
                       Connection *nmap,
                       char *invitation)
{
    char buffer[CONN_BUFSIZE];
    int ccode;
    int size;
    
    size = strlen(invitation);

    ccode = NMAPCreateCollection(nmap, "/invitations/calendar", NULL);
    if (ccode != 4226 && ccode != 1000) {
        return ccode;
    }
    
    ccode = NMAPRunCommandF(nmap, 
                            buffer,
                            CONN_BUFSIZE, "WRITE /invitations/calendar %d %d\r\n", 
                            STORE_DOCTYPE_CALENDAR,
                            size);

    if (ccode != 2002) {
        return ccode;
    }

    ccode = ConnWrite(nmap, invitation, size);
    ConnFlush(nmap);

    if (ccode < 0) {
        return ccode;
    }

    ccode = NMAPReadAnswer(nmap, buffer, CONN_BUFSIZE, TRUE);

    return ccode;
}

static BOOL
ApplyObjects(ItipAgentClient *client, 
             BongoSList *users, 
             BongoCalObject *cal,
             char *invitation)
{
    BongoSList *l;

    for (l = users; l != NULL; l = l->next) {
        const char *user = l->data;
        Connection *conn;

        // FIXME: don't assume localhost.
        conn = NMAPConnect("127.0.0.1", NULL);
        if (!conn || !NMAPAuthenticateThenUserAndStore(conn, user)) {
            continue;
        }
        
        if (cal) {
            ApplyCalObject(client, conn, cal);
        }
        
        if (invitation) {
            SaveCalendarInvitation(client, conn, invitation);
        }
    
        NMAPQuit(conn);
        ConnFree(conn);
    }

    return TRUE;
}

static BOOL
CheckEnvelope(ItipAgentClient *client, char *envelope)
{
    char *ptr;

    ptr = envelope;

    /* Queue-agent specific code.  This code just prints out the envelope */
    while (*ptr) {
        switch(*ptr++) {
        case QUEUE_RECIP_LOCAL :
        case QUEUE_RECIP_MBOX_LOCAL :
            return TRUE;
            break;
        }
        BONGO_ENVELOPE_NEXT(ptr);
    }

    return FALSE;
}

static void
ReadEnvelope(ItipAgentClient *client, char *envelope, BongoSList **usersOut)
{
    char *ptr;
    char *delim;
    char *user;

    *usersOut = NULL;
    ptr = envelope;

    /* Queue-agent specific code.  This code just prints out the envelope */
    while (*ptr) {
        switch(*ptr++) {
        case QUEUE_RECIP_LOCAL :
        case QUEUE_RECIP_MBOX_LOCAL :
            delim = strchr(ptr, ' ');
            if (delim) {
                user = MemStrndup(ptr, delim - ptr);
            } else {
                user = MemStrdup(ptr);
            }
            *usersOut = BongoSListPrepend(*usersOut, user);
        }
        BONGO_ENVELOPE_NEXT(ptr);
    }
}

static int 
ProcessEntry(void *clientp,
             Connection *conn)
{
    int ccode;
    int envelopeLength;
    char qID[16];
    ItipAgentClient *client = clientp;
    BongoSList *users;
    BongoCalObject *cal;
    char *invitation;

    client->conn = conn;

    ccode = BongoQueueAgentHandshake(client->conn, 
                                    client->line, 
                                    qID, 
                                    &envelopeLength);

    if (ccode == -1) {
        return -1;
    }

    client->envelope = BongoQueueAgentReadEnvelope(client->conn, 
                                                  client->line, 
                                                  envelopeLength);

    if (client->envelope == NULL) {
        return -1;
    }

    if (!CheckEnvelope(client, client->envelope)) {
        /* No reason to check for calendar attachment */
        return 0;
    }

    if (!GetObjects(client, qID, &cal, &invitation)) {
        /* No relevant attachments */
        return 0;
    }

    ReadEnvelope(client, client->envelope, &users);

    ApplyObjects(client, users, cal, invitation);

    BongoSListFreeDeep(users);
    
    /* The client struct, connection, and QDONE will be cleaned up by
     * the caller */
    return 0;
}

static void 
ItipAgentServer(void *ignored)
{
    XplRenameThread(XplGetThreadID(), AGENT_DN " Server");

    /* Listen for incoming queue items.  Call ProcessEntry with a
     * ItipAgentClient allocated for each incoming queue entry. */
    BongoQueueAgentListenWithClientPool(&ItipAgent.agent,
                                       ItipAgent.nmapConn,
                                       ItipAgent.threadPool,
                                       sizeof(ItipAgentClient),
                                       ItipAgentClientFree, 
                                       ProcessEntry,
                                       &ItipAgent.clientPool);

    /* Shutting down */
    XplConsolePrintf(AGENT_NAME ": Shutting down.\r\n");

    if (ItipAgent.nmapConn) {
        ConnClose(ItipAgent.nmapConn, 1);
        ItipAgent.nmapConn = NULL;
    }

    BongoThreadPoolShutdown(ItipAgent.threadPool);
    BongoAgentShutdown(&ItipAgent.agent);

    XplConsolePrintf(AGENT_NAME ": Shutdown complete\r\n");
}

static BOOL 
ReadConfiguration(void)
{
    MDBValueStruct *config;

    config = MDBCreateValueStruct(ItipAgent.agent.directoryHandle, 
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
    BongoAgentHandleSignal(&ItipAgent.agent, sigtype);
}

XplServiceCode(SignalHandler)

int
XplServiceMain(int argc, char *argv[])
{
    int ccode;
    int minThreads;
    int maxThreads;
    int minSleep;
    int startupOpts;

    if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf(AGENT_NAME ": Could not drop to unprivileged user '%s'\r\n" AGENT_NAME ": exiting.\n", MsgGetUnprivilegedUser());
        return -1;
    }
    XplInit();

    strcpy(ItipAgent.nmapAddress, "127.0.0.1");

    /* Initialize the Bongo libraries */
    startupOpts = BA_STARTUP_MDB | BA_STARTUP_CONNIO | BA_STARTUP_NMAP;
    ccode = BongoAgentInit(&ItipAgent.agent, AGENT_NAME, AGENT_DN, DEFAULT_CONNECTION_TIMEOUT, startupOpts);
    if (ccode == -1) {
        XplConsolePrintf(AGENT_NAME ": Exiting.\r\n");
        return -1;
    }

    BongoCalInit(MsgGetDBFDir(NULL));

    /* Set up socket for listening on an incoming queue */ 
    ItipAgent.queueNumber = Q_FIVE;
    ItipAgent.nmapConn = BongoQueueConnectionInit(&ItipAgent.agent, ItipAgent.queueNumber);

    if (!ItipAgent.nmapConn) {
        BongoAgentShutdown(&ItipAgent.agent);
        XplConsolePrintf(AGENT_NAME ": Exiting.\r\n");
        return -1;
    }

    BongoQueueAgentGetThreadPoolParameters(&ItipAgent.agent,
                                          &minThreads, &maxThreads, &minSleep);
    
    /* Create a thread pool for managing connections */
    ItipAgent.threadPool = BongoThreadPoolNew(AGENT_DN " Clients",
                                          BONGO_QUEUE_AGENT_DEFAULT_STACK_SIZE,
                                          minThreads, maxThreads, minSleep);

    if (ItipAgent.threadPool == NULL) {
        BongoAgentShutdown(&ItipAgent.agent);
        XplConsolePrintf(AGENT_NAME ": Unable to create thread pool.\r\n" AGENT_NAME ": Exiting.\r\n");
        return -1;
    }
    
    ReadConfiguration();

    XplSignalHandler(SignalHandler);

    /* Start the server thread */
    XplStartMainThread(AGENT_NAME, &id, ItipAgentServer, 8192, NULL, ccode);
    
    return 0;
}
