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

#include "queue.h"
#include "queued.h"

#include <xpl.h>
#include <memmgr.h>
#include <bongoutil.h>
#include <bongoagent.h>
#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include <connio.h>

#include "conf.h"
#include "domain.h"
#include "messages.h"

QueueAgent Agent = { {0, }, };

static int CommandAuth(void *param);
static int CommandPass(void *param);
static int CommandQuit(void *param);
static int CommandHelp(void *param);
static int CommandAddressResolve(void *param);
int aliasFindFunc(const void *str, const void *node);

static ProtocolCommand authCommands[] = {
    { NMAP_AUTH_COMMAND, NMAP_AUTH_HELP, sizeof(NMAP_AUTH_COMMAND) - 1, CommandAuth, NULL, NULL },
    { NMAP_HELP_COMMAND, NMAP_HELP_HELP, sizeof(NMAP_HELP_COMMAND) - 1, CommandHelp, NULL, NULL },
    { NMAP_PASS_COMMAND, NMAP_PASS_HELP, sizeof(NMAP_PASS_COMMAND) - 1, CommandPass, NULL, NULL },
    { NMAP_QUIT_COMMAND, NMAP_QUIT_HELP, sizeof(NMAP_QUIT_COMMAND) - 1, CommandQuit, NULL, NULL },
    { NMAP_ADDRESS_RESOLVE_COMMAND, NMAP_ADDRESS_RESOLVE_HELP, sizeof(NMAP_ADDRESS_RESOLVE_COMMAND) -1, CommandAddressResolve, NULL, NULL },
    { NULL, NULL, 0, NULL, NULL, NULL }
};

static ProtocolCommand commands[] = {
    { NMAP_HELP_COMMAND, NMAP_HELP_HELP, sizeof(NMAP_HELP_COMMAND) - 1, CommandHelp, NULL, NULL },
    { NMAP_QADDM_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QADDM_COMMAND) - 1, CommandQaddm, NULL, NULL }, 
    { NMAP_QADDQ_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QADDQ_COMMAND) - 1, CommandQaddq, NULL, NULL }, 
    { NMAP_QABRT_COMMAND, NMAP_QABRT_HELP, sizeof(NMAP_QABRT_COMMAND) - 1, CommandQabrt, NULL, NULL }, 
    { NMAP_QBODY_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QBODY_COMMAND) - 1, CommandQbody, NULL, NULL }, 
    { NMAP_QBRAW_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QBRAW_COMMAND) - 1, CommandQbraw, NULL, NULL }, 
    { NMAP_QCOPY_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QCOPY_COMMAND) - 1, CommandQcopy, NULL, NULL }, 
    { NMAP_QCREA_COMMAND, NMAP_QCREA_HELP, sizeof(NMAP_QCREA_COMMAND) - 1, CommandQcrea, NULL, NULL }, 
    { NMAP_QDELE_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QDELE_COMMAND) - 1, CommandQdele, NULL, NULL }, 
    { NMAP_QDONE_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QDONE_COMMAND) - 1, CommandQdone, NULL, NULL }, 
    { NMAP_QDSPC_COMMAND, NMAP_QDSPC_HELP, sizeof(NMAP_QDSPC_COMMAND) - 1, CommandQdspc, NULL, NULL }, 
    { NMAP_QGREP_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QGREP_COMMAND) - 1, CommandQgrep, NULL, NULL }, 
    { NMAP_QHEAD_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QHEAD_COMMAND) - 1, CommandQhead, NULL, NULL }, 
    { NMAP_QINFO_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QINFO_COMMAND) - 1, CommandQinfo, NULL, NULL }, 
    { NMAP_QMOD_FROM_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QMOD_FROM_COMMAND) - 1, CommandQmodFrom, NULL, NULL }, 
    { NMAP_QMOD_FLAGS_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QMOD_FLAGS_COMMAND) - 1, CommandQmodFlags, NULL, NULL }, 
    { NMAP_QMOD_LOCAL_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QMOD_LOCAL_COMMAND) - 1, CommandQmodLocal, NULL, NULL }, 
    { NMAP_QMOD_MAILBOX_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QMOD_MAILBOX_COMMAND) - 1, CommandQmodMailbox, NULL, NULL }, 
    { NMAP_QMOD_RAW_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QMOD_RAW_COMMAND) - 1, CommandQmodRaw, NULL, NULL }, 
    { NMAP_QMOD_TO_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QMOD_TO_COMMAND) - 1, CommandQmodTo, NULL, NULL }, 
    { NMAP_QMIME_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QMIME_COMMAND) - 1, CommandQmime, NULL, NULL }, 
    { NMAP_QMOVE_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QMOVE_COMMAND) - 1, CommandQmove, NULL, NULL }, 
    { NMAP_QRCP_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QRCP_COMMAND) - 1, CommandQrcp, NULL, NULL }, 
    { NMAP_QRETR_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QRETR_COMMAND) - 1, CommandQretr, NULL, NULL }, 
    { NMAP_QRTS_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QRTS_COMMAND) - 1, CommandQrts, NULL, NULL }, 
    { NMAP_QRUN_COMMAND, NMAP_QRUN_HELP, sizeof(NMAP_QRUN_COMMAND) - 1, CommandQrun, NULL, NULL }, 
    { NMAP_QSRCH_DOMAIN_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QSRCH_DOMAIN_COMMAND) - 1, CommandQsrchDomain, NULL, NULL }, 
    { NMAP_QSRCH_HEADER_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QSRCH_HEADER_COMMAND) - 1, CommandQsrchHeader, NULL, NULL }, 
    { NMAP_QSRCH_BODY_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QSRCH_BODY_COMMAND) - 1, CommandQsrchBody, NULL, NULL }, 
    { NMAP_QSRCH_BRAW_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QSRCH_BRAW_COMMAND) - 1, CommandQsrchBraw, NULL, NULL }, 
    { NMAP_QSTOR_ADDRESS_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QSTOR_ADDRESS_COMMAND) - 1, CommandQstorAddress, NULL, NULL }, 
    { NMAP_QSTOR_CAL_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QSTOR_CAL_COMMAND) - 1, CommandQstorCal, NULL, NULL }, 
    { NMAP_QSTOR_FLAGS_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QSTOR_FLAGS_COMMAND) - 1, CommandQstorFlags, NULL, NULL }, 
    { NMAP_QSTOR_FROM_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QSTOR_FROM_COMMAND) - 1, CommandQstorFrom, NULL, NULL }, 
    { NMAP_QSTOR_LOCAL_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QSTOR_LOCAL_COMMAND) - 1, CommandQstorLocal, NULL, NULL }, 
    { NMAP_QSTOR_MESSAGE_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QSTOR_MESSAGE_COMMAND) - 1, CommandQstorMessage, NULL, NULL }, 
    { NMAP_QSTOR_RAW_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QSTOR_RAW_COMMAND) - 1, CommandQstorRaw, NULL, NULL }, 
    { NMAP_QSTOR_TO_COMMAND, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_QSTOR_TO_COMMAND) - 1, CommandQstorTo, NULL, NULL }, 
    { NMAP_QUIT_COMMAND, NMAP_QUIT_HELP, sizeof(NMAP_QUIT_COMMAND) - 1, CommandQuit, NULL, NULL },
    { NMAP_QWAIT_COMMAND, NMAP_QWAIT_HELP, sizeof(NMAP_QWAIT_COMMAND) - 1, CommandQwait, NULL, NULL }, 
    { NMAP_QFLUSH_COMMAND, NMAP_QFLUSH_HELP, sizeof(NMAP_QFLUSH_COMMAND) -1, CommandQflush, NULL, NULL },
    { NULL, NULL, 0, NULL, NULL, NULL }
};

int aliasFindFunc(const void *str, const void *node) {
    struct _AliasStruct *n = (struct _AliasStruct *)(node);
    return strcasecmp((char *)str, n->original);
}

/* TODO: error handling on MsgParseAddress() */
BOOL aliasing(Connection *conn, char *addr, int *cnt) {
    unsigned char *local = NULL;
    unsigned char *domain = NULL;
    struct _AliasStruct a;
    int i=-1;
    BOOL result=FALSE;

    /* very rudimentary way to prevent loops */
    if (*cnt > 5) {
        ConnWriteStr(conn, "4000 Aliasing loop\r\n");
        return TRUE;
    }

    (*cnt)++;

    /* first parse out the domain do that i can run over the hosted_domains array */
    MsgParseAddress(addr, strlen(addr), &local, &domain);

    i = BongoArrayFindSorted(Conf.aliasList, domain, (ArrayCompareFunc)aliasFindFunc);
    if (i > -1) {
        a = BongoArrayIndex(Conf.aliasList, struct _AliasStruct, i);
        /* if there is a to domain use that instead */
        if (a.to) {
            MemFree(domain);
            domain = MemStrdup(a.to);
        }

        /* now i need to see if there is an alias for local within this domain */
        if (a.aliases) {
            i = BongoArrayFindSorted(a.aliases, local, (ArrayCompareFunc)aliasFindFunc);
            if (i > -1) {
                /* there is an alias for this local.  recurse, as the destination might not be local */
                struct _AliasStruct b;
                b = BongoArrayIndex(a.aliases, struct _AliasStruct, i);
                result = aliasing(conn, b.to, cnt);
            }
        }

        /* we haven't already handled this address during the recursion.  it must be local */
        if (!result) {
            ConnWriteF(conn, "1000 LOCAL %s@%s\r\n", local, domain);
            result = TRUE;
        }
    } else {
        /* we don't host this domain in any way, it is a remote domain.  just echo it back */
        ConnWriteF(conn, "1000 REMOTE %s\r\n", addr);
        result = TRUE;
    }

    if (local) {
        MemFree(local);
    }
    if (domain) {
        MemFree(domain);
    }
    return result;
}

int CommandAddressResolve(void *param) {
    BOOL handled=FALSE;
    int cnt=0;
    QueueClient *client = (QueueClient *)param;

    handled = aliasing(client->conn, client->buffer + 16, &cnt);
    if (!handled) {
        ConnWriteF(client->conn, "4000 UNKNOWN %s\r\n", client->buffer + 16);
    }
    return 0;
}

int 
CommandAuth(void *param)
{
    unsigned char *ptr;
    unsigned char credential[XPLHASH_MD5_LENGTH];
    QueueClient *client = (QueueClient *)param;
    xpl_hash_context context;

    ptr = client->buffer + 5;
    if (strncmp("SYSTEM ", ptr, 7)) {
        return ConnWriteStr(client->conn, MSG3000UNKNOWN);
    }
    ptr += 7;
    
    XplHashNew(&context, XPLHASH_MD5);
    XplHashWrite(&context, client->authChallenge, strlen(client->authChallenge));
    XplHashWrite(&context, Conf.serverHash, NMAP_HASH_SIZE);
    XplHashFinal(&context, XPLHASH_LOWERCASE, credential, XPLHASH_MD5_LENGTH);

    if (QuickCmp(credential, ptr)) {
        client->authorized = TRUE;
        return(ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1));
    }

    Log(LOG_NOTICE, "SYSTEM AUTH failed from host %s", LOGIP(client->conn->socketAddress));

    ConnWrite(client->conn, MSG3242BADAUTH, sizeof(MSG3242BADAUTH) - 1);

    XplDelay(2000);

    return(-1);
}

int 
CommandPass(void *param)
{
    unsigned char *ptr;
    unsigned char *pass;
    BOOL result;
    struct sockaddr_in nmap;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 4;

    if (*ptr++ == ' ') {
        if (       (toupper(ptr[0]) == 'U') 
                && (toupper(ptr[1]) == 'S') 
                && (toupper(ptr[2]) == 'E') 
                && (toupper(ptr[3]) == 'R') 
                && (ptr[4] == ' ') 
                && (ptr[5] != '\0') 
                && (!isspace(ptr[5])) 
                && ((pass = strchr(ptr + 5, ' ')) != NULL)) {
            ptr += 5;
            *pass++ = '\0';

            result = MsgAuthVerifyPassword(ptr, pass);

            if (result && (nmap.sin_addr.s_addr == MsgGetHostIPAddress())) {
                return(ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1));
            } else {
                return(ConnWrite(client->conn, MSG4120USERLOCKED, sizeof(MSG4120USERLOCKED) - 1));
            }

            Log(LOG_NOTICE, "PASS USER failed for user from host %s", LOGIP(client->conn->socketAddress));
        } else if (    (toupper(ptr[0]) == 'S') 
                    && (toupper(ptr[1]) == 'Y') 
                    && (toupper(ptr[2]) == 'S') 
                    && (toupper(ptr[3]) == 'T') 
                    && (toupper(ptr[4]) == 'E') 
                    && (toupper(ptr[5]) == 'M') 
                    && (ptr[6] == ' ')) {
            ptr += 7;

#if defined(DEBUG)
            if (strcmp(Conf.serverHash, ptr) == 0) {
                client->authorized = TRUE;

                return(ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1));
            }
#endif

            Log(LOG_NOTICE, "PASS SYSTEM failed from host %s", LOGIP(client->conn->socketAddress));
        }
    }

    XplDelay(2000);

    ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1);

    client->done = TRUE;
    
    return(-1);
}

static int 
CommandQuit(void *param)
{
    QueueClient *client = param;
    
    client->done = TRUE;

    return -1;
}

static int 
CommandHelp(void *param)
{
    int ccode;
    unsigned char *ptr;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 4;
    if (*ptr == '\0') {
        ccode = ConnWrite(client->conn, MSG2001HELP, sizeof(MSG2001HELP) - 1);
    } else if (*ptr++ == ' ') {
        ProtocolCommand *command;

        if (client->authorized) {
            command = ProtocolCommandTreeSearch(&Agent.commands, ptr);
        } else {
            command = ProtocolCommandTreeSearch(&Agent.authCommands, ptr);
        }
        
        if (command) {
            if (command->help) {
                ccode = ConnWrite(client->conn, command->help, strlen(command->help));
            } else {
                ccode = ConnWrite(client->conn, NMAP_HELP_NOT_DEFINED, sizeof(NMAP_HELP_NOT_DEFINED) - 1);
            }
        } else {
            return(ConnWrite(client->conn, MSG3000UNKNOWN, sizeof(MSG3000UNKNOWN) - 1));
        }
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    if (ccode != -1) {
        ccode = ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1);
    }

    return(ccode);
}

void 
QueueClientFree(void *clientp)
{
    QueueClient *client = clientp;

    MemPrivatePoolReturnEntry(client);
}

static BOOL 
CheckTrustedHost(QueueClient *client)
{
    int i = 0;
    
    if (Conf.trustedHosts) {
        /* TODO: this can be optimized a little bit */
        XplRWReadLockAcquire(&Conf.lock);
        i = BongoArrayFindSorted(Conf.trustedHosts, inet_ntoa(client->conn->socketAddress.sin_addr), (ArrayCompareFunc)strcasecmp);
        XplRWReadLockRelease(&Conf.lock);
    }
    return (i > 0);
}

int
HandleCommand(QueueClient *client)
{
    int ccode = 0;
    
    while (Agent.agent.state < BONGO_AGENT_STATE_STOPPING
           && client && client->conn && !client->done) {
        client->buffer[0] = '\0';

        ccode = ConnReadAnswer(client->conn, client->buffer, CONN_BUFSIZE);

        if ((ccode != -1) && (ccode < CONN_BUFSIZE)) {            
            ProtocolCommand *command;

            if (client->authorized) {
                command = ProtocolCommandTreeSearch(&Agent.commands, client->buffer);
            } else {
                command = ProtocolCommandTreeSearch(&Agent.authCommands, client->buffer);                
            }
            
            if (command) {
                ccode = command->handler(client);
            } else {
                if (client->authorized) {
                    ccode = ConnWrite(client->conn, MSG3000UNKNOWN, sizeof(MSG3000UNKNOWN) - 1);
                } else {
                    ccode = ConnWrite(client->conn, MSG3240NOAUTH, sizeof(MSG3240NOAUTH) - 1);
                }
                
                Log(LOG_INFO, "Handled command from %s", LOGIP(client->conn->socketAddress));
            }
        } else if (ccode == -1) {
            break;
        }

        if ((ccode = ConnFlush(client->conn)) == -1) {
            break;
        }
    }

    return(ccode);  
}

static int
ProcessClient(void *clientp, Connection *conn) 
{
    QueueClient *client = clientp;
    client->conn = conn;
    int ccode;

    client->authorized = CheckTrustedHost(client);

    if (!client->authorized) {
        sprintf(client->authChallenge, "%x%s%x", (unsigned int)XplGetThreadID(), Conf.hostname, (unsigned int)time(NULL));
        ccode = ConnWriteF(client->conn, MSG4242AUTHREQUIRED, client->authChallenge);
    } else {
        ccode = ConnWriteF(client->conn, "1000 %s %s\r\n", Conf.hostname, MSG1000READY);
    }
    
    if (ccode == -1) {
        return -1;
    }
    
    if ((ccode = ConnFlush(client->conn)) == -1) {
        return -1;
    }

    return HandleCommand(client);
}

static void 
QueueServer(void *ignored)
{
    XplRenameThread(XplGetThreadID(), AGENT_DN " Server");

    XplSafeIncrement(Agent.activeThreads);

    BongoAgentListenWithClientPool(&Agent.agent,
                                  Agent.clientListener,
                                  Agent.clientThreadPool,
                                  sizeof(QueueClient),
                                  INT_MAX,
                                  QueueClientFree,
                                  ProcessClient,
                                  &Agent.clientMemPool);

    /* Shutting down */
    if (Agent.clientListener) {
        ConnClose(Agent.clientListener, 1);
        Agent.clientListener = NULL;
    }

#if VERBOSE
    XplConsolePrintf("bongoqueue: Closing queue database\r\n");
#endif

    QueueShutdown();

    XplRWLockDestroy(&Conf.lock);

#if VERBOSE
    /* Shutting down */
    XplConsolePrintf(AGENT_NAME ": Shutting down.\r\n");
#endif

    BongoThreadPoolShutdown(Agent.clientThreadPool);

    MsgClearRecoveryFlag("queue");

    BongoAgentShutdown(&Agent.agent);

#if VERBOSE
    XplConsolePrintf(AGENT_NAME ": Shutdown complete\r\n");
#endif
}


static Connection *
ServerSocketInit(int port)
{
    Connection *conn = ConnAlloc(FALSE);
    if (conn) {
        conn->socketAddress.sin_family = AF_INET;
        conn->socketAddress.sin_port = htons(port);
        conn->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();

        /* Get root privs back for the bind.  It's ok if this fails - 
         * the user might not need to be root to bind to the port */
        XplSetEffectiveUserId(0);

        conn->socket = ConnServerSocket(conn, 2048);

        if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
            XplConsolePrintf("bongoqueue: Could not drop to unprivileged user '%s'\n", MsgGetUnprivilegedUser());
            ConnFree(conn);
            return NULL;
        }

        if (conn->socket == -1) {
            XplConsolePrintf("bongoqueue: Could not bind to port %d\n", port);
            ConnFree(conn);
            return NULL;
        }
    } else {
        XplConsolePrintf("bongoqueue: Could not allocate connection\n");
        return NULL;
    }

    return conn;
}


static void
CheckDiskspace(BongoAgent *agent)
{
    unsigned long freeBlocks;
    unsigned long bytesPerBlock;
    unsigned long wantFree;

    bytesPerBlock = XplGetDiskBlocksize(Conf.spoolPath);
    freeBlocks = XplGetDiskspaceFree(Conf.spoolPath);        

    wantFree = (Conf.minimumFree / bytesPerBlock) + 1;
    if (freeBlocks != 0x7f000000) {
        if (freeBlocks < wantFree) {
            /* BUG ALERT: */
            /* With a block size of 4KB, freeBlocks could */
            /* overflow if a volume had more than 16 TB. */

            Agent.flags |= QUEUE_AGENT_DISK_SPACE_LOW;
            
            Log(LOG_ERROR, "Disk space for %s low: have %ld free, want %ld.", Conf.spoolPath, freeBlocks, wantFree);
        } else {
            Agent.flags &= ~QUEUE_AGENT_DISK_SPACE_LOW;
        }
    } else {
        Log(LOG_NOTICE, "Couldn't work out disk space free for %s", Conf.spoolPath);
    }
}

static void
CheckLoad(BongoAgent *agent)
{
    unsigned long current;
    unsigned long state = 0;
    unsigned long upTime = 0;
    unsigned long idleTime = 0;

    if (!Conf.loadMonitorDisabled) {
        current = XplGetServerUtilization(&upTime, &idleTime);
        
        if (current <= Conf.loadMonitorLow) {
            if (state) {
                if (Conf.defaultConcurrentWorkers > Conf.maxConcurrentWorkers) {
                    Conf.maxConcurrentWorkers *= 2;
                }
                
                state = 0;
            } else {
                if (Conf.defaultConcurrentWorkers > Conf.maxConcurrentWorkers) {
                    Conf.maxConcurrentWorkers *= 2;
                }
                
                if (Conf.defaultSequentialWorkers > Conf.maxSequentialWorkers) {
                    Conf.maxSequentialWorkers *= 2;
                }
            }
            Conf.queueCheck = CalculateCheckQueueLimit(Conf.maxConcurrentWorkers, Conf.maxSequentialWorkers);
        } else if (current >= Conf.loadMonitorHigh) {
            if (XplSafeRead(Queue.queuedLocal) >= Conf.limitTrigger) {
                if (state) {
                    Conf.maxSequentialWorkers = max(Conf.maxSequentialWorkers / 2, 14);
                    state = 0;
                } else {
                    Conf.maxConcurrentWorkers = max(Conf.maxConcurrentWorkers / 2, 7);
                    state = 1;
                }
            }
            Conf.queueCheck = CalculateCheckQueueLimit(Conf.maxConcurrentWorkers, Conf.maxSequentialWorkers);
        }
    }    
}

static void 
SignalHandler(int sigtype) 
{
    BongoAgentHandleSignal(&Agent.agent, sigtype);
}

XplServiceCode(SignalHandler)

int
XplServiceMain(int argc, char *argv[])
{
    BOOL recover;
    int ccode;
    int startupOpts;

    LogStart();

    if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf(AGENT_NAME ": Could not drop to unprivileged user '%s'\r\n" AGENT_NAME ": exiting.\n", MsgGetUnprivilegedUser());
        return -1;
    }
    
    XplInit();

    /* Set the default port */
    Agent.agent.port = BONGO_QUEUE_PORT;

    /* Initialize the Bongo libraries */
    startupOpts = BA_STARTUP_CONNIO | BA_STARTUP_NMAP;
    ccode = BongoAgentInit(&Agent.agent, AGENT_NAME, MSGSRV_AGENT_QUEUE, DEFAULT_CONNECTION_TIMEOUT, startupOpts);
    if (ccode == -1) {
        XplConsolePrintf(AGENT_NAME ": Exiting.\r\n");
        return -1;
    }
    
    XplRWLockInit(&Conf.lock);
    XplSafeWrite(Agent.activeThreads, 0);

    ReadConfiguration(&recover);

    BongoAgentAddConfigMonitor(&Agent.agent, CheckConfig);
    BongoAgentAddDiskMonitor(&Agent.agent, CheckDiskspace);
    BongoAgentAddLoadMonitor(&Agent.agent, CheckLoad);

    Agent.clientListener = ServerSocketInit(Agent.agent.port);
    if (Agent.clientListener == NULL) {
        XplConsolePrintf(AGENT_NAME ": Exiting.\r\n");
        return -1;
    }

    if (QueueInit() == FALSE) {
        XplConsolePrintf(AGENT_NAME ": Exiting.\r\n");
        return -1;
    }

    LoadProtocolCommandTree(&Agent.authCommands, authCommands);
    LoadProtocolCommandTree(&Agent.commands, commands);

    /* FIXME: We want to pool work on the command level rather than
     * the connection level.  Until then the threadpool will have a
     * thread per client, so no (practical) limit */
    Agent.clientThreadPool = BongoThreadPoolNew("Queue Clients", STACKSPACE_Q, 2, INT_MAX, 0);

    XplSignalHandler(SignalHandler);

    ccode = CreateQueueThreads(recover);

    BongoAgentStartMonitor(&Agent.agent);

    MsgSetRecoveryFlag("queue");

    /* Start the server thread */
    XplStartMainThread(AGENT_NAME, &id, QueueServer, 8192, NULL, ccode);

    LogShutdown();
    
    XplUnloadApp(XplGetThreadID());
    
    return 0;
}

