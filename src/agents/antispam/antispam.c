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

/** \file antispam.c Code for the anti-spam agent
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

#include "antispam.h"

#define ASpamClientAlloc() MemPrivatePoolGetEntryDirect(ASpam.QueueMemPool, __FILE__, __LINE__)

static void SignalHandler(int sigtype);
static void AntispamServer(void *ignored);

ASpamGlobals ASpam;

static void 
ASpamClientFree(void *client)
{
    register ASpamClient *c = client;

/* this get's free'd by QueueHandlerConnection()
    if (c->conn) {
        ConnClose(c->conn, 1);
        ConnFree(c->conn);
        c->conn = NULL;
    }
*/

    if (c->envelope) {
        MemFree(c->envelope);
    }
    MemPrivatePoolReturnEntry(ASpam.QueueMemPool, c);

    return;
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
    ASpamClient *client = clientp;
    char *envelopeLine;
    BOOL hasFlags = FALSE;
    unsigned long msgFlags = 0;
    int ccode;

    unsigned long source = 0;
    char *ptr;
    char *ptr2;
    char *senderUserName = NULL;
    BOOL copy;
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
                    *ptr2 = ' ';
                }
            }
            break;
        case QUEUE_FLAGS:
            hasFlags = TRUE;
            copy = FALSE;
            msgFlags = atol(envelopeLine+1);
            if (msgFlags & MSG_FLAG_SPAM_CHECKED) {
                /* we've already scanned this.  don't do it again */
                goto done;
            }
            break;
        case QUEUE_ADDRESS:
            source = atol(envelopeLine+1);
            break;
        }
        BONGO_ENVELOPE_NEXT(envelopeLine);
    }

    blocked = SpamdCheck(&ASpam.spamd, client, client->qID, hasFlags, msgFlags);

done:

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

static BOOL 
ReadConfiguration(void) {
    char *pconfig;
    BOOL retcode = FALSE;
    BongoJsonNode *node;


    if (! ReadBongoConfiguration(GlobalConfig, "global")) {
        return FALSE;
    }

    if (! NMAPReadConfigFile("antispam", &pconfig)) {
        printf("manager: couldn't read config from store\n");
        return FALSE;
    }
    if (! ReadBongoConfiguration(GlobalConfig, "global")) {
        Log(LOG_ERROR, "Unable to read Global configration from the store.");
        exit(-1);
    }

    if (BongoJsonParseString(pconfig, &node) != BONGO_JSON_OK) {
        printf("manager: couldn't parse JSON config\n");
        goto finish;
    }

    SpamdReadConfiguration(&ASpam.spamd);

    retcode = TRUE;
finish:
    BongoJsonNodeFree(node);
    return retcode;
}

#if defined(NETWARE) || defined(LIBC) || defined(WIN32)
static int 
_NonAppCheckUnload(void)
{
    static BOOL    checked = FALSE;
    XplThreadID    id;

    if (!checked) {
        checked = TRUE;
        ASpam.state = ASPAM_STATE_UNLOADING;

        XplWaitOnLocalSemaphore(ASpam.sem.shutdown);

        id = XplSetThreadGroupID(ASpam.id.group);
        ConnClose(ASpam.nmap.conn, 1);
        XplSetThreadGroupID(id);

        XplWaitOnLocalSemaphore(ASpam.sem.main);
    }

    return(0);
}
#endif

static void 
SignalHandler(int sigtype)
{
    switch(sigtype) {
        case SIGHUP: {
            if (ASpam.state < ASPAM_STATE_UNLOADING) {
                ASpam.state = ASPAM_STATE_UNLOADING;
            }

            break;
        }

        case SIGINT:
        case SIGTERM: {
            if (ASpam.state == ASPAM_STATE_STOPPING) {
                XplUnloadApp(getpid());
            } else if (ASpam.state < ASPAM_STATE_STOPPING) {
                ASpam.state = ASPAM_STATE_STOPPING;
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
AntispamServer(void *ignored) {
    int minThreads, maxThreads, minSleep;

    UNUSED_PARAMETER(ignored)

    BongoQueueAgentGetThreadPoolParameters(&ASpam.agent, &minThreads, &maxThreads, &minSleep);
    ASpam.QueueThreadPool = BongoThreadPoolNew(AGENT_NAME " Clients", BONGO_QUEUE_AGENT_DEFAULT_STACK_SIZE, minThreads, maxThreads, minSleep);
    ASpam.QueueConnection = BongoQueueConnectionInit(&ASpam.agent, Q_INCOMING);

    BongoQueueAgentListenWithClientPool(&ASpam.agent,
                                            ASpam.QueueConnection,
                                            ASpam.QueueThreadPool,
                                            sizeof(ASpamClient),
                                            ASpamClientFree,
                                            ProcessConnection,
                                            &ASpam.QueueMemPool);
    BongoThreadPoolShutdown(ASpam.QueueThreadPool);
    ConnClose(ASpam.QueueConnection);
    BongoAgentShutdown(&ASpam.agent);
}

int
XplServiceMain()
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
    ccode = BongoAgentInit(&ASpam.agent, AGENT_NAME, DEFAULT_CONNECTION_TIMEOUT, startupOpts);
    if ( ccode == -1) {
        LogFailureF("%s: Exiting", AGENT_NAME);
        return -1;
    }

    ReadConfiguration();
    ASpam.nmap.ssl.config.key.file = MsgGetFile(MSGAPI_FILE_PRIVKEY, NULL, 0);
    ASpam.nmap.ssl.config.certificate.file = MsgGetFile(MSGAPI_FILE_PUBKEY, NULL, 0);
    ASpam.nmap.ssl.config.key.type = GNUTLS_X509_FMT_PEM;
    ASpam.nmap.ssl.context = ConnSSLContextAlloc(&(ASpam.nmap.ssl.config));

    SpamdStartup(&(ASpam.spamd));

    XplSignalHandler(SignalHandler);

    /* start the server thread */
    XplStartMainThread(AGENT_NAME, &id, AntispamServer, 8192, NULL, ccode);
    
    XplUnloadApp(XplGetThreadID());
    return 0;
}
