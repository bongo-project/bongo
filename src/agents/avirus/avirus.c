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
 * (C) 2007 Patrick Felt
 ****************************************************************************/

/** \file avirus.c Code for the anti-virus agent
 */

#include <config.h>

#include <xpl.h>
#include <memmgr.h>

#include <bongoagent.h>
#include <bongoutil.h>
#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include <streamio.h>
#include <connio.h>
#include <logger.h>

#include "avirus.h"

static void SignalHandler(int sigtype);


AVirusGlobals AVirus;

static void 
AVirusClientFree(void *client)
{
    register AVirusClient *c = client;

    if (c->conn) {
        ConnClose(c->conn, 1);
        ConnFree(c->conn);
        c->conn = NULL;
    }

    if (c->envelope) {
        MemFree(c->envelope);
    }
    MemPrivatePoolReturnEntry(c);

    return;
}

BOOL VirusCheck(AVirusClient *client, const char *queueID, BOOL hasFlags, unsigned long msgFlags, unsigned long senderIp, char *senderUserName) {
    int ccode;
    long size;
    BOOL infected = FALSE;
    Connection *conn;

    conn = ConnAddressPoolConnect(&(AVirus.clamd.hosts), AVirus.clamd.connectionTimeout); 
    if (conn) {
	    Connection *data;
	    unsigned short port;
	    
	    ConnWrite(conn, "STREAM\r\n", strlen("STREAM\r\n"));
	    ConnFlush(conn);
	    ccode = ConnReadAnswer(conn, client->line, CONN_BUFSIZE);

	    if (!ccode || strncmp(client->line, "PORT ", strlen("PORT ")) != 0 || (port = atoi(client->line + strlen("PORT "))) == 0) {
		    ConnFree(conn);
		    return -1;
	    }
	    
	    data = ConnAlloc(TRUE);
	    if (!data) {
		    ConnFree(conn);
		    return -1;
	    }

	    memcpy(&data->socketAddress, &conn->socketAddress, sizeof(struct sockaddr_in));
	    data->socketAddress.sin_port = htons(port);
	    if (ConnConnectEx(data, NULL, 0, NULL, client->conn->trace.destination) < 0) {
		    ConnFree(conn);
		    ConnFree(data);
		    return -1;
	    }

	    size = 0;
	    if (((ccode = NMAPSendCommandF(client->conn, "QRETR %s MESSAGE\r\n", queueID)) != -1)
		&& ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1)
		&& ccode == 2023) {
		    char *ptr;

		    ptr = strchr (client->line, ' ');
		    if (ptr) {
		        *ptr = '\0';
		    }
		
		    size = atol(client->line);
	    }

	    if (size == 0) {
		    ConnFree(conn);
		    ConnFree(data);
		    return -1;
	    }

	    ccode = ConnReadToConn(client->conn, data, size);
	    
	    ConnFree(data);
	    
	    if ((ccode == -1) || ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != 1000)) {
            Log(LOG_DEBUG, "result: %d", ccode);
		    ConnFree(conn);
		    return -1;
	    }

	    while ((ccode = ConnReadAnswer(conn, client->line, CONN_BUFSIZE)) != -1) {
            char *ptr;

            ptr = strrchr(client->line, ' ');
            if (XplStrCaseCmp(ptr + 1, "FOUND") == 0) {
                *ptr = '\0';
                ptr = client->line + strlen("stream: ");

#if 0
                if(client->foundViruses.used == client->foundViruses.allocated) {
                    client->foundViruses.names = MemRealloc(client->foundViruses.names, sizeof(char*) * (client->foundViruses.allocated + MIME_REALLOC_SIZE));
                }
                client->foundViruses.names[client->foundViruses.used++] = MemStrdup(ptr);

                XplSafeIncrement(AVirus.stats.viruses);
#endif
                infected = TRUE;
            }
	    }
	    ConnFree(conn);
    }

    return infected;
}

/** Callback function.  Whenever a new message arrives in the queue that
 * this agent has registered itself on, NMAP calls back to this function
 * and provides the agent with the unique hash of the new message.  The 
 * connection then remains open, and NMAP goes into slave state while
 * the agent does all necessary processing.
 * \param client The connection initiated by the NMAP store.
 */
static __inline int 
ProcessConnection(void *clientp, Connection *conn)
{
    AVirusClient *client = clientp;
    unsigned char *envelopeLine;
    BOOL hasFlags = FALSE;
    unsigned long msgFlags = 0;
    int ccode;

    unsigned long source = 0;
    unsigned char *ptr;
    char *ptr2;
    char *senderUserName = NULL;
    BOOL blocked = FALSE;

    client->conn = conn;
    ccode = BongoQueueAgentHandshake(client->conn, client->line, client->qID, &client->envelopeLength, &client->messageLength);
    client->envelope = BongoQueueAgentReadEnvelope(client->conn, client->line, client->envelopeLength, &client->envelopeLines);

    envelopeLine = client->envelope;
    while (*envelopeLine) {
        switch(*envelopeLine) {
        case QUEUE_FROM:
            if ((ptr = strchr(envelopeLine, ' '))) {
                ptr++;
                if ((ptr2 = strchr(ptr, ' '))) {
                    *ptr2 = '\0';
                    if (strcmp(ptr, "-") != 0) {
                        senderUserName = MemStrdup(ptr);
                    }
                    *ptr = ' ';
                }
            }
            break;
        case QUEUE_FLAGS:
            hasFlags = TRUE;
            msgFlags = atol(envelopeLine+1);
            break;
        case QUEUE_ADDRESS:
            source = atol(envelopeLine+1);
            break;
        }
        BONGO_ENVELOPE_NEXT(envelopeLine);
    }

    blocked = VirusCheck(client, client->qID, hasFlags, msgFlags, source, senderUserName);

    if (blocked == TRUE) {
        /* we found a virus of some kind.  drop the mail */
        /* FIXME: we should generate a notice of some kindshouldn't we? */
        ConnWriteF(client->conn, "QDELE %s\r\n", client->qID);
        ConnFlush(client->conn);
    }
    if ((ccode != -1) 
            && ((ccode = NMAPSendCommand(client->conn, "QDONE\r\n", 7)) != -1)) {
        ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
    }

    if (client->envelope) {
        MemFree(client->envelope);
        client->envelope = NULL;
    }

    if (senderUserName) {
        MemFree(senderUserName);
    }

    return(0);
}

static BongoConfigItem ClamHostList = {
    BONGO_JSON_STRING, NULL, &AVirus.clamd.hostlist
};

static BongoConfigItem AVirusConfigSchema[] = {
    { BONGO_JSON_INT, "o:flags/i", &AVirus.flags },
    { BONGO_JSON_INT, "o:queue/i", &AVirus.nmap.queue },
    { BONGO_JSON_INT, "o:timeout/i", &AVirus.clamd.connectionTimeout },
    { BONGO_JSON_ARRAY, "o:hosts/a", &ClamHostList},
    { BONGO_JSON_NULL, NULL, NULL },
};

/*
{
"version": 1,
"flags": 168,
"patterns": "",
"queue": 5,
"hosts": [ "127.0.0.1:3310:1" ],
"timeout": 15
}
*/

static void
ParseHost(char *buffer, char **host, int *port, int *weight)
{
    char *portPtr;
    char *weightPtr;

    portPtr = NULL;
    weightPtr = NULL;

    if (*buffer == '\0') {
            return;
    }

    if (*buffer == ':') {
        portPtr = buffer;
    } else {
        *host = buffer;
        portPtr = strchr(buffer + 1, ':');
    }

    if (portPtr) {
        *portPtr = '\0';
        portPtr++;
        if (*portPtr == ':') {
            weightPtr = portPtr;
        } else if (*portPtr != '\0') {
            *port = (unsigned short)atol(portPtr);
            weightPtr = strchr(portPtr + 1, ':');
        }
        if (weightPtr) {
            weightPtr++;
            if ( *weightPtr != '\0' ) {
                *weight = (unsigned long)atol(weightPtr);
            } /*else, retain preset default weight */
        }
    }
}

static BOOL 
ReadConfiguration(void) {
    unsigned int i;

    if (! ReadBongoConfiguration(GlobalConfig, "global")) {
        return FALSE;
    }

    if (! ReadBongoConfiguration(AVirusConfigSchema, "antivirus")) {
        return FALSE;
    }

    for (i=0; i < BongoArrayCount(AVirus.clamd.hostlist); i++) {
        char *hostitem = BongoArrayIndex(AVirus.clamd.hostlist, char*, i);
        char *lHost = MemStrdup(hostitem);
        char *host;
        int port, weight;
        ParseHost(lHost, &host, &port, &weight);
        ConnAddressPoolAddHost(&AVirus.clamd.hosts, host, port, weight);
        MemFree(lHost);
    }

    return TRUE;
}

#if defined(NETWARE) || defined(LIBC) || defined(WIN32)
static int 
_NonAppCheckUnload(void)
{
    static BOOL    checked = FALSE;
    XplThreadID    id;

    if (!checked) {
        checked = TRUE;
        AVirus.state = AVIRUS_STATE_UNLOADING;

        XplWaitOnLocalSemaphore(AVirus.sem.shutdown);

        id = XplSetThreadGroupID(AVirus.id.group);
        ConnClose(AVirus.nmap.conn, 1);
        XplSetThreadGroupID(id);

        XplWaitOnLocalSemaphore(AVirus.sem.main);
    }

    return(0);
}
#endif

static void 
SignalHandler(int sigtype)
{
    switch(sigtype) {
        case SIGHUP: {
            if (AVirus.state < AVIRUS_STATE_UNLOADING) {
                AVirus.state = AVIRUS_STATE_UNLOADING;
            }

            break;
        }

        case SIGINT:
        case SIGTERM: {
            if (AVirus.state == AVIRUS_STATE_STOPPING) {
                XplUnloadApp(getpid());
            } else if (AVirus.state < AVIRUS_STATE_STOPPING) {
                AVirus.state = AVIRUS_STATE_STOPPING;
            }

            break;
        }

        default: {
            break;
        }
    }

    return;
}

XplServiceCode(SignalHandler)

static void
AntiVirusServer(void *ignored) {
    int minThreads, maxThreads, minSleep;

    BongoQueueAgentGetThreadPoolParameters(&AVirus.agent, &minThreads, &maxThreads, &minSleep);
    AVirus.QueueThreadPool = BongoThreadPoolNew(AGENT_NAME " Clients", BONGO_QUEUE_AGENT_DEFAULT_STACK_SIZE, minThreads, maxThreads, minSleep);
    AVirus.QueueConnection = BongoQueueConnectionInit(&AVirus.agent, Q_INCOMING);

    BongoQueueAgentListenWithClientPool(&AVirus.agent,
                                            AVirus.QueueConnection,
                                            AVirus.QueueThreadPool,
                                            sizeof(AVirusClient),
                                            AVirusClientFree,
                                            ProcessConnection,
                                            &AVirus.QueueMemPool);
    BongoThreadPoolShutdown(AVirus.QueueThreadPool);
    ConnClose(AVirus.QueueConnection, 1);
    BongoAgentShutdown(&AVirus.agent);
}

int
XplServiceMain(int argc, char *argv[])
{
    int ccode;
    int startupOpts;

    if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
        Log(LOG_ERROR, "Could not drop to unprivileged user '%s'", MsgGetUnprivilegedUser());
        return(1);
    }
    XplInit();

    /* initialize all the libraries we need to */
    startupOpts = BA_STARTUP_CONNIO | BA_STARTUP_NMAP;
    ccode = BongoAgentInit(&AVirus.agent, AGENT_NAME, DEFAULT_CONNECTION_TIMEOUT, startupOpts);
    if ( ccode == -1) {
        LogFailureF("%s: Exiting", AGENT_NAME);
        return -1;
    }

    ReadConfiguration();
    AVirus.nmap.ssl.config.key.file = MsgGetFile(MSGAPI_FILE_PRIVKEY, NULL, 0);
    AVirus.nmap.ssl.config.certificate.file = MsgGetFile(MSGAPI_FILE_PUBKEY, NULL, 0);
    AVirus.nmap.ssl.config.key.type = GNUTLS_X509_FMT_PEM;
    AVirus.nmap.ssl.context = ConnSSLContextAlloc(&(AVirus.nmap.ssl.config));

    XplSignalHandler(SignalHandler);

    /* start the server thread */
    XplStartMainThread(AGENT_NAME, &id, AntiVirusServer, 8192, NULL, ccode);
    
    XplUnloadApp(XplGetThreadID());
    return 0;
}
