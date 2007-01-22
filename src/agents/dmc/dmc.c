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
#include <errno.h>
#include <bongoutil.h>
#include <connio.h>
#include <mdb.h>
#include <msgapi.h>
#include <logger.h>
#include <management.h>

#include "dmcp.h"

#define PRODUCT_NAME "Bongo Distributed Management Console"
#define PRODUCT_SHORT_NAME "DMC"
#define PRODUCT_DESCRIPTION "Provides distributed management access to Bongo modules."
#define PRODUCT_VERSION "$Revision: 1.6 $"

#define PROTOCOL_VERSION        "1.0"

#define DMC_CONNECTION_STACK_SIZE (64 * 1024)
#define DMC_CONNECTION_TIMEOUT (15 * 60)

#define SEND_PROMPT(c) if ((c)->flags & CLIENT_FLAG_PROMPT) { SendClientPrompt((c)); }

#define QUEUE_WORK_TO_DO(c, id, r) \
        { \
            XplWaitOnLocalSemaphore(DMC.client.semaphore); \
            if (XplSafeRead(DMC.client.worker.idle)) { \
                (c)->queue.previous = NULL; \
                if (((c)->queue.next = DMC.client.worker.head) != NULL) { \
                    (c)->queue.next->queue.previous = (c); \
                } else { \
                    DMC.client.worker.tail = (c); \
                } \
                DMC.client.worker.head = (c); \
                (r) = 0; \
            } else if (XplSafeRead(DMC.client.worker.active) < XplSafeRead(DMC.client.worker.maximum)) { \
                XplSafeIncrement(DMC.client.worker.active); \
                XplSignalBlock(); \
                XplBeginThread(&(id), HandleConnection, DMC_CONNECTION_STACK_SIZE, XPL_INT_TO_PTR(XplSafeRead(DMC.client.worker.active)), (r)); \
                XplSignalHandler(DMCSignalHandler); \
                if (!(r)) { \
                    (c)->queue.previous = NULL; \
                    if (((c)->queue.next = DMC.client.worker.head) != NULL) { \
                        (c)->queue.next->queue.previous = (c); \
                    } else { \
                        DMC.client.worker.tail = (c); \
                    } \
                    DMC.client.worker.head = (c); \
                } else { \
                    XplSafeDecrement(DMC.client.worker.active); \
                    (r) = DMC_RECEIVER_OUT_OF_MEMORY; \
                } \
            } else { \
                (r) = DMC_RECEIVER_CONNECTION_LIMIT; \
            } \
            XplSignalLocalSemaphore(DMC.client.semaphore); \
        }

#if !defined(DEBUG)
#define ClientAlloc() MemPrivatePoolGetEntry(DMC.client.pool)
#else
#define ClientAlloc() MemPrivatePoolGetEntryDebug(DMC.client.pool, __FILE__, __LINE__)
#endif

static int DMCBridgedCommandAgent(void *param);
static int DMCBridgedCommandCmd(void *param);
static int DMCBridgedCommandDeregister(void *param);
static int DMCBridgedCommandGet(void *param);
static int DMCBridgedCommandLogin(void *param);
static int DMCBridgedCommandPrompt(void *param);
static int DMCBridgedCommandQuit(void *param);
static int DMCBridgedCommandReset(void *param);
static int DMCBridgedCommandRegister(void *param);
static int DMCBridgedCommandRestart(void *param);
static int DMCBridgedCommandServer(void *param);
static int DMCBridgedCommandSet(void *param);

static int DMCCommandAgent(void *param);
static int DMCCommandAuth(void *param);
static int DMCCommandCmd(void *param);
static int DMCCommandDeregister(void *param);
static int DMCCommandGet(void *param);
static int DMCCommandHeartBeat(void *param);
static int DMCCommandHelp(void *param);
static int DMCCommandLogin(void *param);
static int DMCCommandPrompt(void *param);
static int DMCCommandQuit(void *param);
static int DMCCommandReset(void *param);
static int DMCCommandRegister(void *param);
static int DMCCommandRestart(void *param);
static int DMCCommandServer(void *param);
static int DMCCommandSet(void *param);
static int DMCCommandShutdown(void *param);
static int DMCCommandStart(void *param);
static int DMCCommandStat0(void *param);
static int DMCCommandStat1(void *param);
static int DMCCommandStat2(void *param);
static int DMCCommandStats(void *param);

static ProtocolCommand DMCBridgedCommandEntries[] = {
    { DMC_COMMAND_AGENT, DMC_HELP_AGENT, sizeof(DMC_COMMAND_AGENT) - 1, DMCBridgedCommandAgent, NULL, NULL }, 
    { DMC_COMMAND_CMD, DMC_HELP_CMD, sizeof(DMC_COMMAND_CMD) - 1, DMCBridgedCommandCmd, NULL, NULL }, 
    { DMC_COMMAND_DEREGISTER, DMC_HELP_DEREGISTER, sizeof(DMC_COMMAND_DEREGISTER) - 1, DMCBridgedCommandDeregister, NULL, NULL }, 
    { DMC_COMMAND_GET, DMC_HELP_GET, sizeof(DMC_COMMAND_GET) - 1, DMCBridgedCommandGet, NULL, NULL }, 
    { DMC_COMMAND_HELP, DMC_HELP_HELP, sizeof(DMC_COMMAND_HELP) - 1, DMCCommandHelp, NULL, NULL }, 
    { DMC_COMMAND_LOGIN, DMC_HELP_LOGIN, sizeof(DMC_COMMAND_LOGIN) - 1, DMCBridgedCommandLogin, NULL, NULL }, 
    { DMC_COMMAND_PROMPT, DMC_HELP_PROMPT, sizeof(DMC_COMMAND_PROMPT) - 1, DMCBridgedCommandPrompt, NULL, NULL }, 
    { DMC_COMMAND_QUIT, DMC_HELP_QUIT, sizeof(DMC_COMMAND_QUIT) - 1, DMCBridgedCommandQuit, NULL, NULL }, 
    { DMC_COMMAND_RESET, DMC_HELP_RESET, sizeof(DMC_COMMAND_RESET) - 1, DMCBridgedCommandReset, NULL, NULL }, 
    { DMC_COMMAND_REGISTER, DMC_HELP_REGISTER, sizeof(DMC_COMMAND_REGISTER) - 1, DMCBridgedCommandRegister, NULL, NULL }, 
    { DMC_COMMAND_RESTART, DMC_HELP_RESTART, sizeof(DMC_COMMAND_RESTART) - 1, DMCBridgedCommandRestart, NULL, NULL }, 
    { DMC_COMMAND_SERVER, DMC_HELP_SERVER, sizeof(DMC_COMMAND_SERVER) - 1, DMCBridgedCommandServer, NULL, NULL }, 
    { DMC_COMMAND_SET, DMC_HELP_SET, sizeof(DMC_COMMAND_SET) - 1, DMCBridgedCommandSet, NULL, NULL }, 

    { NULL, NULL, 0, NULL, NULL, NULL }
};

static ProtocolCommand DMCCommandEntries[] = {
    { DMC_COMMAND_AGENT, DMC_HELP_AGENT, sizeof(DMC_COMMAND_AGENT) - 1, DMCCommandAgent, NULL, NULL }, 
    { DMC_COMMAND_AUTH, DMC_HELP_AUTH, sizeof(DMC_COMMAND_AUTH) - 1, DMCCommandAuth, NULL, NULL }, 
    { DMC_COMMAND_CMD, DMC_HELP_CMD, sizeof(DMC_COMMAND_CMD) - 1, DMCCommandCmd, NULL, NULL }, 
    { DMC_COMMAND_DEREGISTER, DMC_HELP_DEREGISTER, sizeof(DMC_COMMAND_DEREGISTER) - 1, DMCCommandDeregister, NULL, NULL }, 
    { DMC_COMMAND_GET, DMC_HELP_GET, sizeof(DMC_COMMAND_GET) - 1, DMCCommandGet, NULL, NULL }, 
    { DMC_COMMAND_HELP, DMC_HELP_HELP, sizeof(DMC_COMMAND_HELP) - 1, DMCCommandHelp, NULL, NULL }, 
    { DMC_COMMAND_HEARTBEAT, NULL, sizeof(DMC_COMMAND_HEARTBEAT) - 1, DMCCommandHeartBeat, NULL, NULL }, 
    { DMC_COMMAND_LOGIN, DMC_HELP_LOGIN, sizeof(DMC_COMMAND_LOGIN) - 1, DMCCommandLogin, NULL, NULL }, 
    { DMC_COMMAND_PROMPT, DMC_HELP_PROMPT, sizeof(DMC_COMMAND_PROMPT) - 1, DMCCommandPrompt, NULL, NULL }, 
    { DMC_COMMAND_QUIT, DMC_HELP_QUIT, sizeof(DMC_COMMAND_QUIT) - 1, DMCCommandQuit, NULL, NULL }, 
    { DMC_COMMAND_RESET, DMC_HELP_RESET, sizeof(DMC_COMMAND_RESET) - 1, DMCCommandReset, NULL, NULL }, 
    { DMC_COMMAND_REGISTER, DMC_HELP_REGISTER, sizeof(DMC_COMMAND_REGISTER) - 1, DMCCommandRegister, NULL, NULL }, 
    { DMC_COMMAND_RESTART, DMC_HELP_RESTART, sizeof(DMC_COMMAND_RESTART) - 1, DMCCommandRestart, NULL, NULL }, 
    { DMC_COMMAND_SERVER, DMC_HELP_SERVER, sizeof(DMC_COMMAND_SERVER) - 1, DMCCommandServer, NULL, NULL }, 
    { DMC_COMMAND_SET, NULL, sizeof(DMC_COMMAND_SET) - 1, DMCCommandSet, NULL, NULL }, 
    { DMC_COMMAND_SHUTDOWN, NULL, sizeof(DMC_COMMAND_SHUTDOWN) - 1, DMCCommandShutdown, NULL, NULL }, 
    { DMC_COMMAND_START, NULL, sizeof(DMC_COMMAND_START) - 1, DMCCommandStart, NULL, NULL }, 
    { DMC_COMMAND_STAT0, NULL, sizeof(DMC_COMMAND_STAT0) - 1, DMCCommandStat0, NULL, NULL }, 
    { DMC_COMMAND_STAT1, NULL, sizeof(DMC_COMMAND_STAT1) - 1, DMCCommandStat1, NULL, NULL }, 
    { DMC_COMMAND_STAT2, NULL, sizeof(DMC_COMMAND_STAT2) - 1, DMCCommandStat2, NULL, NULL }, 
    { DMC_COMMAND_STATS, NULL, sizeof(DMC_COMMAND_STATS) - 1, DMCCommandStats, NULL, NULL }, 

    { NULL, NULL, 0, NULL, NULL, NULL }
};

static void DMCUnloadConfiguration(void);
static void DMCSortAgentsByPriority(DMCAgent **Head, DMCAgent **Tail);
static int DMCReadAnswer(
        Connection *pConn,
        unsigned char *response,
        size_t length,
        BOOL checkForResult);

static BOOL 
ClientAllocCB(void *buffer, void *clientData)
{
    register DMCClient *c = (DMCClient *)buffer;

    c->state = CLIENT_STATE_FRESH;
    c->flags = 0;
    c->server = NULL;
    c->agent = NULL;
    c->identity[0] = c->credential[0] = c->buffer[0] = '\0';
    c->rdnOffset = 0;

    c->user = NULL;
    c->resource = NULL;

    return(TRUE);
}

static void 
ClientFree(DMCClient *client)
{
    register DMCClient *c = client;

    memset(c->identity, 0, sizeof(c->identity));
    memset(c->credential, 0, sizeof(c->credential));

    MemPrivatePoolReturnEntry(c);

    return;
}

static int 
SendClientPrompt(DMCClient *client)
{
    int ccode;

    switch(client->state) {
        case CLIENT_STATE_CLOSING: {
            ccode = ConnWrite(client->user, DMCMSG1103BAD_STATE, sizeof(DMCMSG1103BAD_STATE) - 1);
            break;
        }

        case CLIENT_STATE_FRESH: {
            ccode = ConnWrite(client->user, DMCMSG1100USE_AUTH, sizeof(DMCMSG1100USE_AUTH) - 1);
            break;
        }

        case CLIENT_STATE_AUTHENTICATED: {
            ccode = ConnWriteF(client->user, DMCMSG1101AUTHORIZED, client->identity + client->rdnOffset);
            break;
        }

        case CLIENT_STATE_SERVER: {
            ccode = ConnWriteF(client->user, DMCMSG1102MANAGING_SERVER, client->server->identity + client->server->rdnOffset);
            break;
        }

        case CLIENT_STATE_AGENT: {
            ccode = ConnWriteF(client->user, DMCMSG1102MANAGING_AGENT, client->server->identity + client->server->rdnOffset, client->agent->identity + client->agent->rdnOffset);
            break;
        }

        default: {
            ccode = ConnWrite(client->user, DMCMSG1103BAD_STATE, sizeof(DMCMSG1103BAD_STATE) - 1);
            break;
        }
    }

    return(ccode);
}

static BOOL 
AddManagedVariable(ManagedAgent *agent, unsigned char *name, unsigned long flags)
{
    unsigned long i;
    BOOL result = TRUE;
    ManagedVariable *var = NULL;

    if (agent && name) {
        for (i = 0; i < agent->variable.count; i++) {
            if (strcmp(agent->variable.list[i].name, name ) != 0) {
                continue;
            }

            var = &(agent->variable.list[i]);
            break;
        }

        if (var == NULL) {
            var = (ManagedVariable *)MemRealloc(agent->variable.list, (agent->variable.count + 1) * sizeof(ManagedVariable));
            if (var) {
                agent->variable.list = var;
                agent->variable.list[agent->variable.count].flags = flags;
                agent->variable.list[agent->variable.count++].name = MemStrdup(name);
            } else {
                result = FALSE;
            }
        }
    } else {
        result = FALSE;
    }

    return(result);
}

static BOOL 
AddManagedCommand(ManagedAgent *agent, unsigned char *name)
{
    unsigned long i;
    BOOL result = TRUE;
    unsigned char **cmd = NULL;

    if (agent && name) {
        for (i = 0; i < agent->command.count; i++) {
            if (strcmp(agent->command.name[i], name) != 0) {
                continue;
            }

            cmd = &(agent->command.name[i]);
            break;
        }

        if (cmd == NULL) {
            cmd = (unsigned char **)MemRealloc(agent->command.name, (agent->command.count + 1) * sizeof(unsigned char *));
            if (cmd) {
                agent->command.name = cmd;
                agent->command.name[agent->command.count++] = MemStrdup(name);
            } else {
                result = FALSE;
            }
        }
    } else {
        result = FALSE;
    }

    return(result);
}

static BOOL 
RemoveManagedAgent(unsigned char *signature)
{
    unsigned long i;
    BOOL result = FALSE;
    ManagedServer *server = NULL;

    if (signature) {
        XplRWWriteLockAcquire(&(DMC.lock));

        server = DMC.server.managed.list;
        if (server) {
            for (i = 0; i < server->agents.count; i++) {
                if (strcmp(server->agents.list[i].signature, signature) != 0) {
                    continue;
                }

                server->agents.list[i].flags &= ~AGENT_FLAG_REGISTERED;
                server->agents.registered--;
                result = TRUE;

                break;
            }
        }

        XplRWWriteLockRelease(&(DMC.lock));
    }

    return(result);
}

static BOOL 
AddManagedAgent(DMCClient *client, unsigned char *identity, short port, short sslPort, unsigned char *signature)
{
    int ccode;
    unsigned long i;
    unsigned char *ptr;
    BOOL result;
    ManagedAgent *agent = NULL;
    ManagedServer *server = NULL;

    if (client && identity) {
        XplRWWriteLockAcquire(&(DMC.lock));

        server = DMC.server.managed.list;
        if (server) {
            for (i = 0; i < server->agents.count; i++) {
                if (strcmp(server->agents.list[i].identity, identity) != 0) {
                    continue;
                }

                agent = &(server->agents.list[i]);

                for (i = 0; i < agent->command.count; i++) {
                    for (i = 0; i < agent->command.count; i++) {
                        MemFree(agent->command.name[i]);
                    }

                    if (agent->command.name) {
                        MemFree(agent->command.name);
                        agent->command.name = NULL;
                    }

                    agent->command.count = 0;

                    for (i = 0; i < agent->variable.count; i++) {
                        MemFree(agent->variable.list[i].name);
                    }

                    if (agent->variable.list) {
                        MemFree(agent->variable.list);
                        agent->variable.list = NULL;
                    }

                    agent->variable.count = 0;
                }

                break;
            }

            result = TRUE;
            if (!agent) {
                agent = (ManagedAgent *)MemRealloc(server->agents.list, (server->agents.count + 1) * sizeof(ManagedAgent));
                if (agent) {
                    server->agents.list = agent;
                    agent = &(server->agents.list[server->agents.count++]);

                    strcpy(agent->identity, identity);
                    ptr = strrchr(agent->identity, '\\');
                    if (ptr) {
                        ptr++;
                        agent->rdnOffset = ptr - agent->identity;
                    } else {
                        agent->rdnOffset = 0;
                    }

                    agent->variable.count = 0;
                    agent->variable.list = NULL;

                    agent->command.count = 0;
                    agent->command.name = NULL;
                } else {
                    result = FALSE;
                }
            }

            if (result) {
                strcpy(agent->signature, signature);

                agent->flags = AGENT_FLAG_REGISTERED;
                server->agents.registered++;

                memset(&(agent->address.clear), 0, sizeof(struct sockaddr_in));
                agent->address.clear.sin_addr.s_addr = htonl(INADDR_LOOPBACK);

                if (port != -1) {
                    agent->address.clear.sin_port = htons((unsigned short)port);
                    agent->flags |= AGENT_FLAG_CLEARTEXT;
                } else {
                    agent->address.clear.sin_port = -1;
                    agent->flags &= ~AGENT_FLAG_CLEARTEXT;
                }

                memset(&(agent->address.ssl), 0, sizeof(struct sockaddr_in));
                agent->address.ssl.sin_addr.s_addr = client->user->socketAddress.sin_addr.s_addr;

                if (sslPort != -1) {
                    agent->address.ssl.sin_port = htons((unsigned short)sslPort);
                    agent->flags |= AGENT_FLAG_SECURE;
                } else {
                    agent->address.ssl.sin_port = -1;
                    agent->flags &= ~AGENT_FLAG_SECURE;
                }

                ccode = ConnWrite(client->user, DMCMSG1003SEND_VARIABLES, sizeof(DMCMSG1003SEND_VARIABLES) - 1);
                if (ccode != -1) {
                    ccode = ConnFlush(client->user);
                }

                while ((ccode != -1) && result) {
                    ccode = ConnReadAnswer(client->user, client->buffer, CONN_BUFSIZE);
                    if (ccode != -1) {
                        if ((client->buffer[1] == ' ') && (client->buffer[2] == '"')) {
                            ptr = strchr(client->buffer + 3, '"');
                            if (ptr) {
                                *ptr = '\0';

                                AddManagedVariable(agent, client->buffer + 3, (client->buffer[0] == 'R')? 0: VARIABLE_FLAG_WRITEABLE);
                                continue;
                            }
                        } else if (strcmp(client->buffer, "END") == 0) {
                            break;
                        }
                    }
                }

                ccode = ConnWrite(client->user, DMCMSG1003SEND_COMMANDS, sizeof(DMCMSG1003SEND_COMMANDS) - 1);
                if (ccode != -1) {
                    ccode = ConnFlush(client->user);
                }

                while ((ccode != -1) && result) {
                    ccode = ConnReadAnswer(client->user, client->buffer, CONN_BUFSIZE);
                    if (ccode != -1) {
                        if (strcmp(client->buffer, "END") != 0) {
                            AddManagedCommand(agent, client->buffer);
                            continue;
                        } else {
                            break;
                        }
                    }
                }

                if (ccode == -1) {
                    result = FALSE;
                }
            }
        } else {
            result = FALSE;
        }

        XplRWWriteLockRelease(&(DMC.lock));
    } else {
        result = FALSE;
    }

    return(result);
}

static BOOL 
AddManagedServer(unsigned char *identity, unsigned char *address, short port, short sslPort)
{
    unsigned long i;
    unsigned char *ptr;
    BOOL result = TRUE;
    ManagedServer *server = NULL;

    if (identity && address && port && sslPort) {
        XplRWWriteLockAcquire(&(DMC.lock));

        for (i = 0; i < DMC.server.managed.count; i++) {
            if (strcmp(DMC.server.managed.list[i].identity, identity) != 0) {
                continue;
            }

            server = &(DMC.server.managed.list[i]);
            break;
        }

        if (!server) {
            server = (ManagedServer *)MemRealloc(DMC.server.managed.list, (DMC.server.managed.count + 1) * sizeof(ManagedServer));
            if (server) {
                DMC.server.managed.list = server;
                server = &(DMC.server.managed.list[DMC.server.managed.count]);
                DMC.server.managed.count++;

                server->flags = SERVER_FLAG_REGISTERED;
                server->agents.count = 0;
                server->agents.registered = 0;
                server->agents.list = NULL;

                if (strcmp(DMC.dn, identity) == 0) {
                    server->flags |= SERVER_FLAG_LOCAL;
                }

                strcpy(server->identity, identity);
                ptr = strrchr(server->identity, '\\');
                if (ptr) {
                    ptr++;
                    server->rdnOffset = ptr - server->identity;
                } else {
                    server->rdnOffset = 0;
                }

                time(&server->startTime);
            } else {
                result = FALSE;
            }
        }

        if (result) {
            memset(&(server->address.clear), 0, sizeof(struct sockaddr_in));
            server->address.clear.sin_addr.s_addr = inet_addr(address);

            if (port != -1) {
                server->address.clear.sin_port = htons((unsigned short)port);
                server->flags |= SERVER_FLAG_CLEARTEXT;
            } else {
                server->address.clear.sin_port = -1;
                server->flags &= ~SERVER_FLAG_CLEARTEXT;
            }

            memset(&(server->address.ssl), 0, sizeof(struct sockaddr_in));
            server->address.ssl.sin_addr.s_addr = server->address.clear.sin_addr.s_addr;

            if (sslPort != -1) {
                server->address.ssl.sin_port = htons((unsigned short)sslPort);
                server->flags |= SERVER_FLAG_SECURE;
            } else {
                server->address.ssl.sin_port = -1;
                server->flags &= ~SERVER_FLAG_SECURE;
            }
        }

        XplRWWriteLockRelease(&(DMC.lock));
    } else {
        result = FALSE;
    }

    return(result);
}

static int 
DMCBridgedCommandAgent(void *param)
{
    int ccode;
    DMCClient *client = param;

    switch (client->state) {
        case CLIENT_STATE_CLOSING:
        case CLIENT_STATE_FRESH:
        case CLIENT_STATE_AUTHENTICATED: {
            ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
            break;
        }

        case CLIENT_STATE_SERVER: {
            if (((ccode = ConnWriteF(client->resource, "%s\r\n", client->buffer)) != -1) 
                    && ((ccode = ConnFlush(client->resource)) != -1)) {
                if (client->buffer[5] == ' ') {
                    if (((ccode = ConnReadLine(client->resource, client->buffer, CONN_BUFSIZE)) != -1) 
                            && ((ccode = ConnWrite(client->user, client->buffer, ccode)) != -1) 
                            && (strncmp(client->buffer, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1) == 0)) {
                        client->state = CLIENT_STATE_AGENT;
                    }

                    break;
                }

                /*    Multi-line response    */
                do {
                    if (((ccode = ConnReadLine(client->resource, client->buffer, CONN_BUFSIZE)) != -1) 
                            && ((ccode = ConnWrite(client->user, client->buffer, ccode)) != -1)) {
                        continue;
                    }

                    break;
                } while (strncmp(client->buffer, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1) != 0);
            }

            break;
        }

        case CLIENT_STATE_AGENT: {
            ccode = ConnWrite(client->user, DMCMSG2103RESET, sizeof(DMCMSG2103RESET) - 1);
            break;
        }

        default: {
            ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
            break;
        }
    }

    return(ccode);
}

static int 
DMCBridgedCommandCmd(void *param)
{
    int ccode;
    unsigned long i;
    unsigned long count;
    unsigned char *ptr;
    DMCClient *client = param;

    if (client->state == CLIENT_STATE_AGENT) {
        /* Single line request */
        if (((ccode = ConnWriteF(client->resource, "%s\r\n", client->buffer)) != -1) 
                && ((ccode = ConnFlush(client->resource)) != -1) 
                && ((ccode = ConnReadLine(client->resource, client->buffer, CONN_BUFSIZE)) != -1)) {
            /* Both DMCMSG1001BYTES_COMING and DMCMSG1001RESPONSES_COMING use 1001- as the prefix */
            if (strncmp(client->buffer, DMCMSG1001BYTES_COMING, 5) == 0) {
                ptr = strchr(client->buffer + 5, ' ');
                *ptr = '\0';
                count = atol(client->buffer + 5);
                *ptr = ' ';

                if ((ccode = ConnWrite(client->user, client->buffer, ccode)) != -1) {
                    if (strncmp(ptr + 1, DMCMSG1001BYTES_COMING, sizeof(DMCMSG1001BYTES_COMING) - 6) == 0) {
                        /*    DMCMSG1001BYTES_COMING    */
                        ccode = ConnReadToConn(client->resource, client->user, count);
                    } else {
                        /*    DMCMSG1001RESPONSES_COMMING    */
                        while (count && (ccode != -1)) {
                            if ((ccode = ConnReadLine(client->resource, client->buffer, CONN_BUFSIZE)) != -1) {
                                count--;

                                ccode = ConnWrite(client->user, client->buffer, ccode);

                                continue;
                            }

                            /*    We lost the target; send fake bytes and quit!    */
                            for (i = 0; i < count; i++) {
                                ConnWrite(client->user, DMCMSG2003CONNECTION_CLOSED, sizeof(DMCMSG2003CONNECTION_CLOSED) - 1);
                            }

                            break;
                        }
                    }

                    if ((ccode != -1) && ((ccode = ConnReadLine(client->resource, client->buffer, CONN_BUFSIZE)) != -1)) {
                        ccode = ConnWrite(client->user, client->buffer, ccode);
                    }
                }
            } else {
                /* An error occurred on the other side. */
                ccode = ConnWriteF(client->user, "%s\r\n", client->buffer);
            }
        }
    } else {
        ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
    }

    return(ccode);
}


static int 
DMCBridgedCommandDeregister(void *param)
{
    DMCClient *client = param;

    return(ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1));
}

static int 
DMCBridgedCommandGet(void *param)
{
    int ccode;
    unsigned long i;
    unsigned long count;
    unsigned char *ptr;
    DMCClient *client = param;

    if (client->state == CLIENT_STATE_AGENT) {
        if (((ccode = ConnWriteF(client->resource, "%s\r\n", client->buffer)) != -1) 
                && ((ccode = ConnFlush(client->resource)) != -1) 
                && ((ccode = ConnReadLine(client->resource, client->buffer, CONN_BUFSIZE)) != -1)) {
            /*    Both DMCMSG1001BYTES_COMING and DMCMSG1001RESPONSES_COMING use 1001- as the prefix    */
            if (strncmp(client->buffer, DMCMSG1001BYTES_COMING, 5) == 0) {
                ptr = strchr(client->buffer + 5, ' ');
                *ptr = '\0';
                count = atol(client->buffer + 5);
                *ptr = ' ';

                if ((ccode = ConnWrite(client->user, client->buffer, ccode)) != -1) {
                    if (strncmp(ptr + 1, DMCMSG1001BYTES_COMING, sizeof(DMCMSG1001BYTES_COMING) - 6) == 0) {
                        /* DMCMSG1001BYTES_COMING */
                        ccode = ConnReadToConn(client->resource, client->user, count);
                    } else {
                        /*    DMCMSG1001RESPONSES_COMMING    */
                        while (count && (ccode != -1)) {
                            if ((ccode = ConnReadLine(client->resource, client->buffer, CONN_BUFSIZE)) != -1) {
                                count--;

                                ccode = ConnWrite(client->user, client->buffer, ccode);

                                continue;
                            }

                            /*    We lost the target; send fake bytes and quit!    */
                            for (i = 0; i < count; i++) {
                                ConnWrite(client->user, DMCMSG2003CONNECTION_CLOSED, sizeof(DMCMSG2003CONNECTION_CLOSED) - 1);
                            }

                            break;
                        }
                    }

                    if ((ccode != -1) && ((ccode = ConnReadLine(client->resource, client->buffer, CONN_BUFSIZE)) != -1)) {
                        ccode = ConnWrite(client->user, client->buffer, ccode);
                    }
                }
            } else {
                /* An error occurred on the other side. */
                ccode = ConnWriteF(client->user, "%s\r\n", client->buffer);
            }
        }
    } else {
        ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
    }

    return(ccode);
}

static int 
DMCBridgedCommandLogin(void *param)
{
    DMCClient *client = param;

    return(ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1));
}

static int 
DMCBridgedCommandPrompt(void *param)
{
    DMCClient *client = param;

    return(ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1));
}


static int 
DMCBridgedCommandQuit(void *param)
{
    DMCClient *client = param;

    ConnWrite(client->resource, "QUIT\r\n", 6);
    ConnClose(client->resource, 0);
    client->resource = NULL;

    client->agent = NULL;
    client->server = NULL;
    client->state = CLIENT_STATE_AUTHENTICATED;

    return(ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1));
}

static int 
DMCBridgedCommandReset(void *param)
{
    int ccode;
    DMCClient *client = param;

    switch (client->state) {
        case CLIENT_STATE_CLOSING:
        case CLIENT_STATE_FRESH: 
        case CLIENT_STATE_AUTHENTICATED: {
            ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
            break;
        }

        case CLIENT_STATE_SERVER: {
            ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);

            ConnWrite(client->resource, "QUIT\r\n", 6);
            ConnClose(client->resource, 0);
            client->resource = NULL;

            client->server = NULL;
            client->agent = NULL;
            client->state = CLIENT_STATE_AUTHENTICATED;

            break;
        }

        case CLIENT_STATE_AGENT: {
            if (((ccode = ConnWrite(client->resource, "RESET\r\n", 7)) != -1) 
                    && ((ccode = ConnFlush(client->resource)) != -1) 
                    && ((ccode = ConnReadAnswer(client->resource, client->buffer, CONN_BUFSIZE)) != -1) 
                    && (strcmp(client->buffer, DMCMSG1000OK) == 0)) {
                client->state = CLIENT_STATE_SERVER;

                ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
                break;
            }

            break;
        }

        default: {
            ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
            break;
        }
    }

    return(ccode);
}


static int 
DMCBridgedCommandRegister(void *param)
{
    DMCClient *client = param;

    return(ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1));
}


static int 
DMCBridgedCommandRestart(void *param)
{
    DMCClient *client = param;

    return(ConnWrite(client->user, DMCMSG2000NOT_IMPLEMENTED, sizeof(DMCMSG2000NOT_IMPLEMENTED) - 1));
}


static int 
DMCBridgedCommandServer(void *param)
{
    DMCClient *client = param;

    return(ConnWrite(client->user, DMCMSG2103RESET, sizeof(DMCMSG2103RESET) - 1));
}


static int 
DMCBridgedCommandSet(void *param)
{
    int ccode;
    unsigned long count;
    unsigned char *ptr;
    DMCClient *client = param;

    if (client->state == CLIENT_STATE_AGENT) {
        if (((ccode = ConnWriteF(client->resource, "%s\r\n", client->buffer)) != -1) 
                && ((ccode = ConnFlush(client->resource)) != -1) 
                && ((ccode = ConnReadLine(client->resource, client->buffer, CONN_BUFSIZE)) != -1) 
                && ((ccode = ConnWrite(client->user, client->buffer, ccode)) != -1) 
                && ((ccode = ConnFlush(client->user)) != -1)) {
            if (strncmp(client->buffer, DMCMSG1003SEND_BYTES, 5) == 0) {
                ptr = strchr(client->buffer + 10, ' ');
                *ptr = '\0';
                count = atol(client->buffer + 10);
                *ptr = ' ';

                if (((ccode = ConnReadToConn(client->user, client->resource, count)) != -1) 
                        && ((ccode = ConnFlush(client->resource)) != -1) 
                        && ((ccode = ConnReadLine(client->resource, client->buffer, CONN_BUFSIZE)) != -1)) {
                    ccode = ConnWrite(client->user, client->buffer, ccode);
                }
            } else {
                ccode = ConnWrite(client->user, client->buffer, ccode);
            }
        }
    } else {
        ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
    }

    return(ccode);
}

static int 
BridgeConnection(void *param)
{
    int ccode;
    DMCClient *client = param;

    if ((ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1)) != -1) {
        ccode = ConnFlush(client->user);
    }

    while ((ccode != -1) && (client->state >= CLIENT_STATE_AUTHENTICATED) && (DMC.state == DMC_RUNNING)) {
        ccode = ConnReadAnswer(client->user, client->buffer, CONN_BUFSIZE);

        if ((ccode != -1) && (ccode < CONN_BUFSIZE)) {
#if 0
            XplConsolePrintf("[%d.%d.%d.%d] Bridging command: %s %s\r\n", 
                    client->user->socketAddress.sin_addr.s_net, 
                    client->user->socketAddress.sin_addr.s_host, 
                    client->user->socketAddress.sin_addr.s_lh, 
                    client->user->socketAddress.sin_addr.s_impno, 
                    client->buffer, 
                    (client->user->ssl.enable)? "(Secure)": "");
#endif

            client->command = ProtocolCommandTreeSearch(&DMC.bridged, client->buffer);
            if (client->command) {
                ccode = client->command->handler(client);
            } else {
                ccode = ConnWrite(client->user, DMCMSG2100BADCOMMAND, sizeof(DMCMSG2100BADCOMMAND) - 1);

                LoggerEvent(DMC.handles.logging, LOGGER_SUBSYSTEM_UNHANDLED, LOGGER_EVENT_UNHANDLED_REQUEST, LOG_INFO, 0, client->identity, client->buffer, XplHostToLittle(client->user->socketAddress.sin_addr.s_addr), 0, NULL, 0);
            }
        } else {
            ccode = ConnWrite(client->user, DMCMSG2100BADCOMMAND, sizeof(DMCMSG2100BADCOMMAND) - 1);

            LoggerEvent(DMC.handles.logging, LOGGER_SUBSYSTEM_UNHANDLED, LOGGER_EVENT_UNHANDLED_REQUEST, LOG_INFO, 0, client->identity, client->buffer, XplHostToLittle(client->user->socketAddress.sin_addr.s_addr), 0, NULL, 0);
        }

        ConnFlush(client->user);

        continue;
    }

    if (client->resource) {
        ConnWrite(client->resource, "QUIT\r\n", 6);
        ConnFlush(client->resource);
        ConnClose(client->resource, 0);
        client->resource = NULL;
    }

    client->server = NULL;
    client->state = CLIENT_STATE_AUTHENTICATED;

    return(ccode);
}

static void 
ParseAddressBookStats(unsigned char *version, unsigned char *data, AddressBookStatistics *stats)
{
    strcpy(stats->Version, version);
    sscanf(data, "%lu:%lu:%lu:%lu:%lu:%lu\r\n", 
            &(stats->Memory.Count), &(stats->Memory.Size), &(stats->Memory.Pitches), &(stats->Memory.Strikes), 
            &(stats->ServerThreads), &(stats->Connections));
}

static void 
ParseAliasStats(unsigned char *version, unsigned char *data, AliasStatistics *stats)
{
    strcpy(stats->Version, version);
    sscanf(data, "%lu:%lu:%lu:%lu:%lu:%lu\r\n", 
            &(stats->Memory.Count), &(stats->Memory.Size), &(stats->Memory.Pitches), &(stats->Memory.Strikes), 
            &(stats->ServerThreads), &(stats->Connections));

    return;
}

static void 
ParseAntiSpamStats(unsigned char *version, unsigned char *data, AntiSPAMStatistics *stats)
{
    strcpy(stats->Version, version);
    sscanf(data, "%lu:%lu:%lu:%lu:%lu:%lu:%lu\r\n", 
            &(stats->Memory.Count), &(stats->Memory.Size), &(stats->Memory.Pitches), &(stats->Memory.Strikes), 
            &(stats->ServerThreads), &(stats->Connections), &(stats->Idle));

    return;
}

static void 
ParseAntiVirusStats(unsigned char *version, unsigned char *data, AntiVirusStatistics *stats)
{
    strcpy(stats->Version, version);
    sscanf(data, "%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu\r\n", 
            &(stats->Memory.Count), &(stats->Memory.Size), &(stats->Memory.Pitches), &(stats->Memory.Strikes), 
            &(stats->ServerThreads), &(stats->Connections), &(stats->Idle), 
            &(stats->MessagesScanned), &(stats->AttachmentsScanned), 
            &(stats->AttachmentsBlocked), &(stats->VirusesFound));

    return;
}

static void 
ParseCalendarStats(unsigned char *version, unsigned char *data, CalendarStatistics *stats)
{
    strcpy(stats->Version, version);
    sscanf(data, "%lu:%lu:%lu:%lu:%lu:%lu:%lu\r\n", 
            &(stats->Memory.Count), &(stats->Memory.Size), &(stats->Memory.Pitches), &(stats->Memory.Strikes), 
            &(stats->ServerThreads), &(stats->Connections), &(stats->Idle));

    return;
}

static void 
ParseGateKeeperStats(unsigned char *version, unsigned char *data, ConnectionManagerStatistics *stats)
{
    strcpy(stats->Version, version);
    sscanf(data, "%lu:%lu:%lu:%lu:%lu:%lu\r\n", 
            &(stats->Memory.Count), &(stats->Memory.Size), &(stats->Memory.Pitches), &(stats->Memory.Strikes), 
            &(stats->ServerThreads), &(stats->Connections));

    return;
}

static void 
ParseFingerStats(unsigned char *version, unsigned char *data, FingerStatistics *stats)
{
    strcpy(stats->Version, version);
    sscanf(data, "%lu:%lu:%lu:%lu:%lu:%lu\r\n", 
            &(stats->Memory.Count), &(stats->Memory.Size), &(stats->Memory.Pitches), &(stats->Memory.Strikes), 
            &(stats->ServerThreads), &(stats->Connections));

    return;
}

static void 
ParseIMAPStats(unsigned char *version, unsigned char *data, IMAPStatistics *stats)
{
    strcpy(stats->Version, version);
    sscanf(data, "%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu\r\n", 
            &(stats->Memory.Count), &(stats->Memory.Size), &(stats->Memory.Pitches), &(stats->Memory.Strikes), 
            &(stats->ServerThreads), &(stats->Connections), &(stats->Idle), 
            &(stats->ConnectionLimit), &(stats->ConnectionsServiced), &(stats->BadPasswords));

    return;
}

static void 
ParseListStats(unsigned char *version, unsigned char *data, ListServerStatistics *stats)
{
    strcpy(stats->Version, version);
    sscanf(data, "%lu:%lu:%lu:%lu:%lu:%lu\r\n", 
            &(stats->Memory.Count), &(stats->Memory.Size), &(stats->Memory.Pitches), &(stats->Memory.Strikes), 
            &(stats->ServerThreads), &(stats->Connections));

    return;
}

static void 
ParseNMAPStats(unsigned char *version, unsigned char *data, NMAPStatistics *stats)
{
    strcpy(stats->Version, version);
    sscanf(data, "%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%d:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu\r\n", 
        &(stats->Memory.Count), &(stats->Memory.Size), &(stats->Memory.Pitches), &(stats->Memory.Strikes), 
        &(stats->ServerThreads), &(stats->Connections), &(stats->Idle), &(stats->QueueThreads), &(stats->Monitor.Disabled), 
        &(stats->Monitor.Interval), &(stats->Utilization.Low), 
        &(stats->Utilization.High), &(stats->Queue.Concurrent), 
        &(stats->Queue.Sequential), &(stats->Queue.Threshold), 
        &(stats->Bounce.Interval), &(stats->Bounce.Maximum), 
        &(stats->NMAP.Connections), &(stats->NMAP.Serviced), 
        &(stats->NMAP.Agents), &(stats->Messages.Queued), 
        &(stats->Messages.SPAMDiscarded), &(stats->Messages.Stored.Count), 
        &(stats->Messages.Stored.Recipients), &(stats->Messages.Stored.KiloBytes), 
        &(stats->Messages.Failed.Local), &(stats->Messages.Failed.Remote));

    return;
}

static void 
ParsePlusPackStats(unsigned char *version, unsigned char *data, PlusPackStatistics *stats)
{
    strcpy(stats->Version, version);
    sscanf(data, "%lu:%lu:%lu:%lu:%lu:%lu\r\n", 
            &(stats->Memory.Count), &(stats->Memory.Size), &(stats->Memory.Pitches), &(stats->Memory.Strikes), 
            &(stats->ServerThreads), &(stats->Connections));

    return;
}

static void 
ParsePOP3Stats(unsigned char *version, unsigned char *data, POP3Statistics *stats)
{
    strcpy(stats->Version, version);
    sscanf(data, "%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu\r\n", 
            &(stats->Memory.Count), &(stats->Memory.Size), &(stats->Memory.Pitches), &(stats->Memory.Strikes), 
            &(stats->ServerThreads), &(stats->Connections), &(stats->Idle), 
            &(stats->ConnectionLimit), &(stats->ConnectionsServiced), &(stats->BadPasswords));

    return;
}

static void 
ParseProxyStats(unsigned char *version, unsigned char *data, ProxyStatistics *stats)
{
    strcpy(stats->Version, version);
    sscanf(data, "%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu\r\n", 
            &(stats->Memory.Count), &(stats->Memory.Size), &(stats->Memory.Pitches), &(stats->Memory.Strikes), 
            &(stats->ServerThreads), &(stats->Connections), &(stats->Idle), 
            &(stats->ConnectionsServiced), &(stats->BadPasswords), 
            &(stats->Messages), &(stats->KiloBytes));

    return;
}

static void 
ParseRulesStats(unsigned char *version, unsigned char *data, RulesServerStatistics *stats)
{
    strcpy(stats->Version, version);
    sscanf(data, "%lu:%lu:%lu:%lu:%lu:%lu:%lu\r\n", 
            &(stats->Memory.Count), &(stats->Memory.Size), &(stats->Memory.Pitches), &(stats->Memory.Strikes), 
            &(stats->ServerThreads), &(stats->Connections), &(stats->Idle));

    return;
}

static void 
ParseSMTPStats(unsigned char *version, unsigned char *data, SMTPStatistics *stats)
{
    strcpy(stats->Version, version);
    sscanf(data, "%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu:%lu\r\n", 
            &(stats->Memory.Count), &(stats->Memory.Size), &(stats->Memory.Pitches), &(stats->Memory.Strikes), 
            &(stats->ServerThreads), &(stats->Connections), &(stats->Idle), 
            &(stats->ConnectionLimit), &(stats->QueueThreads), 
            &(stats->BadPasswords), &(stats->Servers.InBound.Connections), 
            &(stats->Servers.InBound.Serviced), &(stats->Servers.OutBound.Connections), 
            &(stats->Servers.OutBound.Serviced), &(stats->NMAP.Connections), 
            &(stats->Blocked.Address), &(stats->Blocked.MAPS), 
            &(stats->Blocked.NoDNSEntry), &(stats->Blocked.Routing), 
            &(stats->Blocked.Queue), &(stats->Local.Count), 
            &(stats->Local.Recipients), &(stats->Local.KiloBytes), 
            &(stats->Remote.Recipients.Received), &(stats->Remote.Recipients.Stored), 
            &(stats->Remote.KiloBytes.Received), &(stats->Remote.KiloBytes.Stored), 
            &(stats->Remote.Messages.Received), &(stats->Remote.Messages.Stored));

    return;
}

static BOOL 
ReadServerStatistics(DMCClient *client, BongoStatistics *stats)
{
    int ccode;
    int count;
    int responseSize = 0;
    unsigned long i;
    unsigned long cmd;
    unsigned char *ptr;
    unsigned char *data;
    unsigned char *response = NULL;
    BOOL result;

    if (stats) {
        memset(stats, 0, sizeof(BongoStatistics));

        client->server = NULL;
        for (i = 0; i < DMC.server.managed.count; i++) {
            if (DMC.server.managed.list[i].flags & SERVER_FLAG_LOCAL) {
                client->server = &(DMC.server.managed.list[i]);
                break;
            }
        }
    } else {
        return(FALSE);
    }

    if (client->server) {
        stats->UpTime = time(NULL) - DMC.startUpTime;

        client->resource = ConnAlloc(TRUE);
    } else {
        return(FALSE);
    }

    if (!client->resource) {
        return(FALSE);
    }

    result = FALSE;
    for (i = 0; i < client->server->agents.count; i++) {
        client->agent =  &(client->server->agents.list[i]);

        result = FALSE;
        for (cmd = 0; cmd < client->agent->command.count; cmd++) {
            if ((client->agent->flags & AGENT_FLAG_REGISTERED) && (XplStrCaseCmp(client->agent->command.name[cmd], DMCMC_STATS) == 0)) {
                result = TRUE;
                break;
            }
        }

        if (result) {
            stats->ModuleCount++;

            client->resource->socketAddress.sin_family = AF_INET;
            client->resource->socketAddress.sin_addr.s_addr = client->agent->address.clear.sin_addr.s_addr;
            if ((client->resource->socketAddress.sin_port = client->agent->address.clear.sin_port) != (unsigned short)-1) {
                result = ConnConnectEx(client->resource, NULL, 0, NULL, client->user->trace.destination);
            } else if (DMC.client.ssl.enable && ((client->resource->socketAddress.sin_port = client->agent->address.ssl.sin_port) != (unsigned short)-1)) {
                result = ConnConnectEx(client->resource, NULL, 0, DMC.client.ssl.context, client->user->trace.destination);
            } else {
                result = FALSE;
            }
        }

        if (result) {
            if (((ccode = ConnReadAnswer(client->resource, client->buffer, CONN_BUFSIZE) != -1)) 
                    && ((result = ConnWriteF(client->resource, "CMD %d\r\n", cmd)) != -1) 
                    && ((result = ConnFlush(client->resource)) != -1) 
                    && ((result = ConnReadAnswer(client->resource, client->buffer, CONN_BUFSIZE)) != -1)
                    && (strncmp(client->buffer, DMCMSG1001BYTES_COMING, 5) == 0)) {
                count = atol(client->buffer + 5);

                do {
                    if (count < responseSize) {
                        ccode = ConnReadCount(client->resource, response, count);

                        response[count] = '\0';
                        break;
                    }

                    ptr = (unsigned char *)MemRealloc(response, count + 384);
                    if (ptr) {
                        responseSize = count + 383;
                        response = ptr;
                        continue;
                    }

                    break;
                } while (ccode != -1);

                if ((ccode != -1) 
                        && ((ccode = ConnReadAnswer(client->resource, client->buffer, CONN_BUFSIZE)) != -1) 
                        && (strncmp(client->buffer, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 3) == 0)) {
                    result = TRUE;
                } else {
                    result = FALSE;
                }
            }
        }

        if (result) {
            result = FALSE;

            data = strchr(response, '\n');
            if (data) {
                *data = '\0';

                if (data[-1] == '\r') {
                    data[-1] = '\0';
                }

                data++;

                result = TRUE;
            }
        }

        if (result) {
            if (strcmp(client->agent->identity + client->agent->rdnOffset, MSGSRV_AGENT_ADDRESSBOOK) == 0) {
                ParseAddressBookStats(response, data, &(stats->AddressBook));
            } else if (strcmp(client->agent->identity + client->agent->rdnOffset, MSGSRV_AGENT_ALIAS) == 0) {
                ParseAliasStats(response, data, &(stats->Alias));
            } else if (strcmp(client->agent->identity + client->agent->rdnOffset, MSGSRV_AGENT_ANTISPAM) == 0) {
                ParseAntiSpamStats(response, data, &(stats->AntiSpam));
            } else if (strcmp(client->agent->identity + client->agent->rdnOffset, MSGSRV_AGENT_ANTIVIRUS) == 0) {
                ParseAntiVirusStats(response, data, &(stats->AntiVirus));
            } else if (strcmp(client->agent->identity + client->agent->rdnOffset, MSGSRV_AGENT_ITIP) == 0) {
                ParseCalendarStats(response, data, &(stats->Calendar));
            } else if (strcmp(client->agent->identity + client->agent->rdnOffset, MSGSRV_AGENT_GATEKEEPER) == 0) {
                ParseGateKeeperStats(response, data, &(stats->ConnectionManager));
            } else if (strcmp(client->agent->identity + client->agent->rdnOffset, MSGSRV_AGENT_FINGER) == 0) {
                ParseFingerStats(response, data, &(stats->Finger));
            } else if (strcmp(client->agent->identity + client->agent->rdnOffset, MSGSRV_AGENT_IMAP) == 0) {
                ParseIMAPStats(response, data, &(stats->IMAP));
            } else if (strcmp(client->agent->identity + client->agent->rdnOffset, MSGSRV_AGENT_LIST) == 0) {
                ParseListStats(response, data, &(stats->List));
            } else if (strcmp(client->agent->identity + client->agent->rdnOffset, MSGSRV_AGENT_STORE) == 0) {
                ParseNMAPStats(response, data, &(stats->NMAP));
            } else if (strcmp(client->agent->identity + client->agent->rdnOffset, PLUSPACK_AGENT) == 0) {
                ParsePlusPackStats(response, data, &(stats->PlusPack));
            } else if (strcmp(client->agent->identity + client->agent->rdnOffset, MSGSRV_AGENT_POP) == 0) {
                ParsePOP3Stats(response, data, &(stats->POP3));
            } else if (strcmp(client->agent->identity + client->agent->rdnOffset, MSGSRV_AGENT_PROXY) == 0) {
                ParseProxyStats(response, data, &(stats->Proxy));
            } else if (strcmp(client->agent->identity + client->agent->rdnOffset, MSGSRV_AGENT_RULESRV) == 0) {
                ParseRulesStats(response, data, &(stats->Rules));
            } else if (strcmp(client->agent->identity + client->agent->rdnOffset, MSGSRV_AGENT_SMTP) == 0) {
                ParseSMTPStats(response, data, &(stats->SMTP));
            }
        }

        ConnWrite(client->resource, "QUIT\r\n", 6);
        ConnClose(client->resource, 0);
    }

    if (response) {
        MemFree(response);
    }

    return(result);
}

static int 
DMCCommandAgent(void *param)
{
    int ccode;
    int count;
    unsigned long i;
    unsigned char *ptr;
    ManagedAgent *agent;
    ManagedServer *server;
    DMCClient *client = param;

    ccode = -1;
    switch (client->state) {
        case CLIENT_STATE_CLOSING:
        case CLIENT_STATE_FRESH:
        case CLIENT_STATE_AUTHENTICATED: {
            ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
            break;
        }

        case CLIENT_STATE_SERVER: {
            server = client->server;

            if (client->buffer[5] == ' ') {
                if (client->buffer[6] == '"') {
                    ptr = strchr(client->buffer + 7, '"');
                    if (ptr) {
                        *ptr = '\0';

                        agent = NULL;
                        for (i = 0; i < server->agents.count; i++) {
                            if (XplStrCaseCmp(server->agents.list[i].identity + server->agents.list[i].rdnOffset, client->buffer + 7) == 0) {
                                agent = &(server->agents.list[i]);
                                break;
                            }
                        }

                        if (agent) {
                            if (agent->flags & AGENT_FLAG_REGISTERED) {
                                client->resource = ConnAlloc(TRUE);
                                if (client->resource) {
                                    memset(&(client->resource->socketAddress), 0, sizeof(client->resource->socketAddress));
                                    client->resource->socketAddress.sin_family = AF_INET;
                                    client->resource->socketAddress.sin_addr.s_addr = agent->address.clear.sin_addr.s_addr;
                                    if ((client->resource->socketAddress.sin_port = agent->address.clear.sin_port) != (unsigned short)-1) {
                                        ccode = ConnConnectEx(client->resource, NULL, 0, NULL, client->user->trace.destination);
                                    } else if (DMC.client.ssl.enable && ((client->resource->socketAddress.sin_port = agent->address.ssl.sin_port) != (unsigned short)-1)) {
                                        ccode = ConnConnectEx(client->resource, NULL, 0, DMC.client.ssl.context, client->user->trace.destination);
                                    } else {
                                        ccode = ConnWrite(client->user, DMCMSG2105PORT_DISABLED, sizeof(DMCMSG2105PORT_DISABLED) - 1);
                                        break;
                                    }
                                } else {
                                    ccode = ConnWrite(client->user, DMCMSG2000NOMEMORY, sizeof(DMCMSG2000NOMEMORY) - 1);
                                    break;
                                }

                                if ((ccode != -1) 
                                        && ((ccode = ConnReadLine(client->resource, client->buffer, CONN_BUFSIZE)) != -1) 
                                        && (strcmp(client->buffer, DMCMSG1000OK) == 0)) {
                                    client->agent = agent;
                                    client->state = CLIENT_STATE_AGENT;
                                    ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
                                    break;
                                }

                                ccode = ConnWrite(client->user, DMCMSG2106CONNECT_FAILED, sizeof(DMCMSG2106CONNECT_FAILED) - 1);

                                ConnWrite(client->resource, "QUIT\r\n", 6);
                                ConnClose(client->resource, 0);
                                client->resource = NULL;

                                break;
                            }

                            ConnWrite(client->user, DMCMSG1104AGENT_DEREGISTERED, sizeof(DMCMSG1104AGENT_DEREGISTERED) - 1);
                            break;
                        }
                    }
                }
                ccode = ConnWrite(client->user, DMCMSG2101BADPARAMS, sizeof(DMCMSG2101BADPARAMS) - 1);
                break;
            }

            count = 0;
            for (i = 0; i < server->agents.count; i++) {
                if (server->agents.list[i].flags & AGENT_FLAG_REGISTERED) {
                    count++;
                }
            }

            ccode = ConnWriteF(client->user, DMCMSG1001RESPONSES_COMMING, count);
            for (i = 0; (ccode != -1) && (i < server->agents.count); i++) {
                if (server->agents.list[i].flags & AGENT_FLAG_REGISTERED) {
                    ccode = ConnWriteF(client->user, "\"%s\"\r\n", server->agents.list[i].identity + server->agents.list[i].rdnOffset);
                }
            }

            if (ccode != -1) {
                ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
            }

            break;
        }

        case CLIENT_STATE_AGENT: {
            ccode = ConnWrite(client->user, DMCMSG2103RESET, sizeof(DMCMSG2103RESET) - 1);
            break;
        }

        default: {
            ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
            break;
        }
    }

    return(ccode);
}

static int 
DMCCommandCmd(void *param)
{
    int ccode;
    int count;
    unsigned long i;
    unsigned char *ptr;
    ManagedAgent *agent;
    DMCClient *client = param;

    if (client->state == CLIENT_STATE_AGENT) {
        agent = client->agent;
        if (agent->flags & AGENT_FLAG_REGISTERED) {
            if (client->buffer[3] == ' ') {
                ptr = strchr(client->buffer + 4, ' ');
                if (ptr) {
                    *ptr++ = '\0';
                }

                count = -1;
                for (i = 0; i < agent->command.count; i++) {
                    if (XplStrCaseCmp(agent->command.name[i], client->buffer + 4) == 0) {
                        count = i;
                        break;
                    }
                }

                if (count != -1) {
                    if (ptr) {
                        ccode = ConnWriteF(client->resource, "CMD %d %s\r\n", count, ptr);
                    } else {
                        ccode = ConnWriteF(client->resource, "CMD %d\r\n", count);
                    }

                    if ((ccode != -1) 
                            && ((ccode = ConnFlush(client->resource)) != -1) 
                            && ((ccode = ConnReadAnswer(client->resource, client->buffer, CONN_BUFSIZE)) != -1) 
                            && (strncmp(client->buffer, DMCMSG1001BYTES_COMING, 5) == 0)) {
                        count = atol(client->buffer + 5);

                        if (((ccode = ccode = ConnWriteF(client->user, DMCMSG1001BYTES_COMING, count)) != -1) 
                                && ((ccode = ConnReadToConn(client->resource, client->user, count)) != -1) 
                                && ((ccode = ConnReadLine(client->resource, client->buffer, CONN_BUFSIZE)) != -1) 
                                && (strcmp(client->buffer, DMCMSG1000OK) == 0)) {
                            ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
                        }
                    } else {
                        ccode = ConnWrite(client->user, DMCMSG2003CONNECTION_CLOSED, sizeof(DMCMSG2003CONNECTION_CLOSED) - 1);

                        client->flags &= ~CLIENT_FLAG_CONNECTED;
                    }
                } else {
                    ccode = ConnWrite(client->user, DMCMSG2101BADPARAMS, sizeof(DMCMSG2101BADPARAMS) - 1);
                }
            } else if (client->buffer[3] != '\0') {
                ccode = ConnWrite(client->user, DMCMSG2101BADPARAMS, sizeof(DMCMSG2101BADPARAMS) - 1);
            } else {
                ccode = ConnWriteF(client->user, DMCMSG1001RESPONSES_COMMING, agent->variable.count);

                for (i = 0; (ccode != -1) && (i < agent->command.count); i++) {
                    ccode = ConnWriteF(client->user, "%s\r\n", agent->command.name[i]);
                }

                if (ccode != -1) {
                    ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
                }
            }
        } else {
            ccode = ConnWrite(client->user, DMCMSG1104AGENT_DEREGISTERED, sizeof(DMCMSG1104AGENT_DEREGISTERED) - 1);
        }
    } else {
        ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
    }

    return(ccode);
}

static int 
DMCCommandDeregister(void *param)
{
    int ccode;
    DMCClient *client = param;

    if (client->state != CLIENT_STATE_FRESH) {
        return(ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1));
    }

    if (client->buffer[10] != ' ') {
        return(ConnWrite(client->user, DMCMSG2101BADPARAMS, sizeof(DMCMSG2101BADPARAMS) - 1));
    }

    if (RemoveManagedAgent(client->buffer + 11)) {
        ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
    } else {
        ccode = ConnWrite(client->user, DMCMSG2101BADPARAMS, sizeof(DMCMSG2101BADPARAMS) - 1);
    }

    return(ccode);
}

#if defined(DEBUG)
static int 
DMCCommandEroc(void *param)
{
    int ccode;
    ManagedAgent *agent;
    DMCClient *client = param;

    if (client->state == CLIENT_STATE_AGENT) {
        agent = client->agent;

        if ((agent->flags & AGENT_FLAG_REGISTERED) 
                && ((ccode = ConnWriteF(client->resource, "%s\r\n", client->buffer)) != -1) 
                && ((ccode = ConnFlush(client->resource)) != -1) 
                && ((ccode = ConnReadLine(client->resource, client->buffer, CONN_BUFSIZE)) != -1)) {
            ccode = ConnWrite(client->user, client->buffer, ccode);
        } else {
            ccode = ConnWrite(client->user, DMCMSG1104AGENT_DEREGISTERED, sizeof(DMCMSG1104AGENT_DEREGISTERED) - 1);
        }
    } else {
        ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
    }

    return(ccode);
}
#endif

static int 
DMCCommandGet(void *param)
{
    int ccode;
    int count;
    unsigned long i;
    unsigned char *ptr;
    ManagedAgent *agent;
    DMCClient *client = param;

    ccode = -1;
    if (client->state == CLIENT_STATE_AGENT) {
        agent =  client->agent;

        if (agent->flags & AGENT_FLAG_REGISTERED) {
            if ((client->buffer[3] == ' ') && (client->buffer[4] == '"')) {
                ptr = strchr(client->buffer + 5, '"');
                if (ptr) {
                    *ptr = '\0';

                    count = -1;
                    for (i = 0; i < agent->variable.count; i++) {
                        if (XplStrCaseCmp(agent->variable.list[i].name, client->buffer + 5) == 0) {
                            count = i;
                            break;
                        }
                    }

                    if (count != -1) {
                        if (ptr[1] != ' ') {
                            ccode = ConnWriteF(client->resource, "READ %d\r\n", count);
                        } else {
                            ccode = ConnWriteF(client->resource, "HELP %d\r\n", count);
                        }

                        if ((ccode != -1) 
                                && ((ccode = ConnFlush(client->resource)) != -1) 
                                && ((ccode = ConnReadAnswer(client->resource, client->buffer, CONN_BUFSIZE)) != -1) 
                                && (strncmp(client->buffer, DMCMSG1001BYTES_COMING, 5) == 0)) {
                            count = atol(client->buffer + 5);

                            if (((ccode = ConnWriteF(client->user, DMCMSG1001BYTES_COMING, count)) != -1) 
                                    && ((ccode = ConnReadToConn(client->resource, client->user, count)) != -1) 
                                    && ((ccode = ConnReadLine(client->resource, client->buffer, CONN_BUFSIZE)) != -1) 
                                    && (strcmp(client->buffer, DMCMSG1000OK) == 0)) {
                                ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
                            } else {
                                ccode = ConnWrite(client->user, DMCMSG2003CONNECTION_CLOSED, sizeof(DMCMSG2003CONNECTION_CLOSED) - 1);

                                client->flags &= ~CLIENT_FLAG_CONNECTED;
                            }
                        }
                    }
                } else {
                    ccode = ConnWrite(client->user, DMCMSG2101BADPARAMS, sizeof(DMCMSG2101BADPARAMS) - 1);
                }
            } else if (client->buffer[3] != '\0') {
                ccode = ConnWrite(client->user, DMCMSG2101BADPARAMS, sizeof(DMCMSG2101BADPARAMS) - 1);
            } else {
                ccode = ConnWriteF(client->user, DMCMSG1001RESPONSES_COMMING, agent->variable.count);

                /* Send the agent's variables */
                for (i = 0; (ccode != -1) && (i < agent->variable.count); i++) {
                    ccode = ConnWriteF(client->user, "[%s] \"%s\"\r\n", (agent->variable.list[i].flags & VARIABLE_FLAG_WRITEABLE)? "WRITE": "READ ", agent->variable.list[i].name);
                }

                if (ccode != -1) {
                    ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
                }
            }
        } else {
            ccode = ConnWrite(client->user, DMCMSG1104AGENT_DEREGISTERED, sizeof(DMCMSG1104AGENT_DEREGISTERED) - 1);
        }
    } else {
        ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
    }

    return(ccode);
}

static int 
DMCCommandHelp(void *param)
{
    int ccode;
    DMCClient *client = param;

    if (client->buffer[4] != '\0') {
        client->command = ProtocolCommandTreeSearch(&DMC.commands, client->buffer + 5);
        if (client->command) {
            if (client->command->help) {
                ccode = ConnWrite(client->user, client->command->help, strlen(client->command->help));
            } else {
                ccode = ConnWrite(client->user, DMC_HELP_NOT_AVAILABLE, sizeof(DMC_HELP_NOT_AVAILABLE) - 1);
            }
        } else {
            return(ConnWrite(client->user, DMCMSG2100BADCOMMAND, sizeof(DMCMSG2100BADCOMMAND) - 1));
        }
    } else {
        ccode = ConnWrite(client->user, DMC_HELP_COMMANDS, sizeof(DMC_HELP_COMMANDS) - 1);
    }

    if (ccode != -1) {
        ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
    }

    return(ccode);
}

static int
DMCCommandHeartBeat(void *param)
{
    DMCClient *pClient = param;

    return(ConnWrite(pClient->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1));
}

static int 
DMCCommandLogin(void *param)
{
    int ccode;
    unsigned char *ptr;
    unsigned char *reply;
    MDBValueStruct *v;
    DMCClient *client = param;

    if (client->state == CLIENT_STATE_FRESH) {
        if (client->buffer[5] == ' ') {
            reply = DMCMSG2101BADPARAMS;

            ptr = strchr(client->buffer + 6, ' ');
            if (ptr) {
                *ptr++ = '\0';

                v = MDBCreateValueStruct(DMC.handles.directory, NULL);
                if (v) {
                    if (MSGFindUser(client->buffer + 6, client->identity, NULL, NULL, v)) {
                        if (MDBVerifyPassword(client->identity, ptr, v)) {
                            /* fixme - We need a better solution for server to server authentication.    */
                            strcpy(client->credential, ptr);

                            ptr = strrchr(client->identity, '\\');
                            if (ptr) {
                                ptr++;
                                client->rdnOffset = ptr - client->identity;
                            } else {
                                client->rdnOffset = 0;
                            }

                            client->state = CLIENT_STATE_AUTHENTICATED;

                            reply = DMCMSG1000OK;
                        }
                    }

                    MDBDestroyValueStruct(v);
                } else {
                    reply = DMCMSG2000NOMEMORY;
                }
            }

            ccode = ConnWrite(client->user, reply, strlen(reply));
        } else {
            ccode = ConnWrite(client->user, DMCMSG2100BADCOMMAND, sizeof(DMCMSG2100BADCOMMAND) - 1);
        }
    } else {
        ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
    }

    return(ccode);
}

static int 
DMCCommandPrompt(void *param)
{
    DMCClient *client = param;

    if (client->flags & CLIENT_FLAG_PROMPT) {
        client->flags &= ~CLIENT_FLAG_PROMPT;
    } else {
        client->flags |= CLIENT_FLAG_PROMPT;
    }

    return(ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1));
}

static int 
DMCCommandQuit(void *param)
{
    DMCClient *client = param;

    if (client->state == CLIENT_STATE_SERVER) {
        if (!(client->server->flags & SERVER_FLAG_LOCAL)) {
            ConnWrite(client->resource, "QUIT\r\n", 6);
            ConnClose(client->resource, 0);
            client->resource = NULL;
        }
    } else if (client->state == CLIENT_STATE_AGENT) {
        ConnWrite(client->resource, "QUIT\r\n", 6);
        ConnClose(client->resource, 0);
        client->resource = NULL;
    }

    client->flags &= ~CLIENT_FLAG_CONNECTED;

    return(ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1));
}

static int 
DMCCommandReset(void *param)
{
    int ccode;
    DMCClient *client = param;

    switch (client->state) {
        case CLIENT_STATE_CLOSING:
        case CLIENT_STATE_FRESH: 
        case CLIENT_STATE_AUTHENTICATED: {
            ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
            break;
        }

        case CLIENT_STATE_SERVER: {
            client->server = NULL;
            client->state = CLIENT_STATE_AUTHENTICATED;

            ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
            break;
        }

        case CLIENT_STATE_AGENT: {
            ConnWrite(client->resource,  "QUIT\r\n", 6);
            ConnFlush(client->resource);
            ConnReadAnswer(client->resource, client->buffer, CONN_BUFSIZE);
            ConnClose(client->resource, 0);
            client->resource = NULL;

            client->agent = NULL;
            client->state = CLIENT_STATE_SERVER;

            ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
            break;
        }

        default: {
            ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
            break;
        }
    }

    return(ccode);
}

static int 
DMCCommandRegister(void *param)
{
    int ccode;
    unsigned int i;
    unsigned long port;
    unsigned long sslPort;
    unsigned char *ptr;
    unsigned char *ptr2;
    unsigned char salt[(8 * sizeof(unsigned long)) + 1];
    unsigned char digest[16];
    unsigned char signature[(sizeof(digest) * 2) + 1];
    unsigned char identity[MDB_MAX_OBJECT_CHARS + 1];
    MD5_CTX mdContext;
    DMCClient *client = param;

    if (client->state == CLIENT_STATE_FRESH) {
        XplGetHighResolutionTime(port);
        srand((unsigned int)port);

        if ((client->buffer[8] == ' ') && (client->buffer[9] == '"')) {
            ptr = strchr(client->buffer + 10, '"');
            if (ptr && (ptr[1] == ' ') && ((ptr[2] == '-') || isdigit(ptr[2]))) {
                *ptr = '\0';
                ptr += 2;
                ptr2 = strchr(ptr, ' ');
                if (ptr2 && ((ptr2[1] == '-') || isdigit(ptr2[1]))) {
                    *ptr2++ = '\0';

                    strcpy(identity, client->buffer + 10);
                    port = atol(ptr);
                    sslPort = atol(ptr2);

                    memset(salt, 0, sizeof(salt));
                    for (i = 0; i < 16; i++) {
                        sprintf(salt + (i * 2), "%02x", (unsigned char)rand());
                    }

                    if (((ccode = ConnWrite(client->user, DMCMSG1002SALT_COMING, sizeof(DMCMSG1002SALT_COMING) - 1)) != -1) 
                            && ((ccode = ConnWriteF(client->user, "%s\r\n", salt)) != -1) 
                            && ((ccode = ConnFlush(client->user)) != -1) 
                            && ((ccode = ConnReadAnswer(client->user, client->buffer, CONN_BUFSIZE)) != -1) 
                            && (strlen(client->buffer) == sizeof(signature) - 1)) {
                        MD5_Init(&mdContext);
                        MD5_Update(&mdContext, salt, strlen(salt));
                        MD5_Update(&mdContext, DMC.credential, NMAP_HASH_SIZE);
                        MD5_Update(&mdContext, identity, strlen(identity));
                        MD5_Final(digest, &mdContext);

                        for (i = 0; i < sizeof(digest); i++) {
                            sprintf(signature + (i * 2),"%02x", digest[i]);
                        }

                        if ((strcmp(signature, client->buffer) == 0) 
                                && (AddManagedAgent(client, identity, (short) port, (short) sslPort, signature) != -1)) {
                            ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
                        } else {
                            ccode = ConnWrite(client->user, DMCMSG2101BADPARAMS, sizeof(DMCMSG2101BADPARAMS) - 1);
                        }
                    }
                } else {
                    ccode = ConnWrite(client->user, DMCMSG2101BADPARAMS, sizeof(DMCMSG2101BADPARAMS) - 1);
                }
            } else {
                ccode = ConnWrite(client->user, DMCMSG2101BADPARAMS, sizeof(DMCMSG2101BADPARAMS) - 1);
            }
        } else {
            ccode = ConnWrite(client->user, DMCMSG2100BADCOMMAND, sizeof(DMCMSG2100BADCOMMAND) - 1);
        }
    } else {
        ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
    }

    return(ccode);
}

static int 
DMCCommandRestart(void *param)
{
    DMCClient *client = param;

    return(ConnWrite(client->user, DMCMSG2000NOT_IMPLEMENTED, sizeof(DMCMSG2000NOT_IMPLEMENTED) - 1));
}

static int 
DMCCommandStat0(void *param)
{
    int ccode;
    unsigned int i;
    unsigned long *statistic;
    BongoStatistics *server = NULL;
    StatisticsStruct *stats;
    DMCClient *client = param;

    if ((client->state == CLIENT_STATE_FRESH) || (client->state == CLIENT_STATE_AUTHENTICATED)) {
        server = (BongoStatistics *)MemMalloc(sizeof(BongoStatistics));
        if (server != NULL) {
            ccode = ReadServerStatistics(client, server);
        } else {
            ccode = ConnWrite(client->user, DMCMSG2000NOMEMORY, sizeof(DMCMSG2000NOMEMORY) - 1);
        }
    } else {
        ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
    }

    if (server && (ccode != -1)) {
        stats = (StatisticsStruct *)MemMalloc(sizeof(StatisticsStruct));
        if (stats) {
            NMSTATS_TO_STATS(server, stats);

            statistic = &(stats->ModulesLoaded);

            ccode = ConnWriteF(client->user, DMCMSG1001RESPONSES_COMMING, sizeof(StatisticsStruct) / sizeof(unsigned long));
            if (ccode != -1) {
                for (i = 0; i < sizeof(StatisticsStruct) / sizeof(unsigned long); i++, statistic++) {
                    ccode = ConnWriteF(client->user, "%lu\r\n", *statistic);
                }
            }

            if (ccode != -1) {
                ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
            }

            MemFree(stats);
        } else {
            ccode = ConnWrite(client->user, DMCMSG2000NOMEMORY, sizeof(DMCMSG2000NOMEMORY) - 1);
        }
    }

    if (server) {
        MemFree(server);
    }

    return(ccode);
}

static int 
DMCCommandStat1(void *param)
{
    int ccode;
    unsigned int i;
    unsigned long *statistic;
    BongoStatistics *server = NULL;
    AntispamStatisticsStruct *spam;
    DMCClient *client = param;

    if ((client->state == CLIENT_STATE_FRESH) || (client->state == CLIENT_STATE_AUTHENTICATED)) {
        server = (BongoStatistics *)MemMalloc(sizeof(BongoStatistics));
        if (server != NULL) {
            ccode = ReadServerStatistics(client, server);
        } else {
            ccode = ConnWrite(client->user, DMCMSG2000NOMEMORY, sizeof(DMCMSG2000NOMEMORY) - 1);
        }
    } else {
        ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
    }

    if (server && (ccode != -1)) {
        spam = (AntispamStatisticsStruct *)MemMalloc(sizeof(AntispamStatisticsStruct));
        if (spam) {
            NMSTATS_TO_SPAMSTATS(server, spam);

            statistic = &(spam->QueueSpamBlocked);

            ccode = ConnWriteF(client->user, DMCMSG1001RESPONSES_COMMING, sizeof(AntispamStatisticsStruct) / sizeof(unsigned long));
            if (ccode != -1) {
                for (i = 0; i < sizeof(AntispamStatisticsStruct) / sizeof(unsigned long); i++, statistic++) {
                    ccode = ConnWriteF(client->user, "%lu\r\n", *statistic);
                }
            }

            ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);

            MemFree(spam);
        } else {
            ccode = ConnWrite(client->user, DMCMSG2000NOMEMORY, sizeof(DMCMSG2000NOMEMORY) - 1);
        }
    }

    if (server) {
        MemFree(server);
    }

    return(ccode);
}

static int 
DMCCommandStat2(void *param)
{
    int ccode;
    unsigned int i;
    unsigned char *buffer;
    unsigned char *ptr;
    unsigned char *ptr2;
    BongoStatistics *server = NULL;
    DMCClient *client = param;

    if ((client->state == CLIENT_STATE_FRESH) || (client->state == CLIENT_STATE_AUTHENTICATED)) {
        server = (BongoStatistics *)MemMalloc(sizeof(BongoStatistics));
        if (server != NULL) {
            ccode = ReadServerStatistics(client, server);
        } else {
            ccode = ConnWrite(client->user, DMCMSG2000NOMEMORY, sizeof(DMCMSG2000NOMEMORY) - 1);
        }
    } else {
        ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
    }

    if (server && (ccode != -1)) {
        buffer = (unsigned char *)MemMalloc((sizeof(BongoStatistics) * 2) + 1);
        if (buffer) {
            ptr = buffer;
            ptr2 = (unsigned char *)server;

            for (i = 0; i < sizeof(BongoStatistics); i++, ptr += 2, ptr2++) {
                sprintf(ptr, "%02X", *ptr2);
            }

            if (((ccode = ConnWriteF(client->user, DMCMSG1001BYTES_COMING, ptr - buffer)) != -1) 
                    && ((ccode = ConnWrite(client->user, buffer, ptr - buffer)) != -1)) {
                ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
            }

            MemFree(buffer);
        } else {
            ccode = ConnWrite(client->user, DMCMSG2000NOMEMORY, sizeof(DMCMSG2000NOMEMORY) - 1);
        }
    }

    if (server) {
        MemFree(server);
    }

    return(ccode);
}

static int 
DMCCommandStats(void *param)
{
    int ccode;
    BongoStatistics *server = NULL;
    DMCClient *client = param;

    if ((client->state == CLIENT_STATE_FRESH) || (client->state == CLIENT_STATE_AUTHENTICATED)) {
        server = (BongoStatistics *)MemMalloc(sizeof(BongoStatistics));
        if (server != NULL) {
            ccode = ReadServerStatistics(client, server);
        } else {
            ccode = ConnWrite(client->user, DMCMSG2000NOMEMORY, sizeof(DMCMSG2000NOMEMORY) - 1);
        }
    } else {
        ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
    }

    if (server && (ccode != -1)) {
        if (server->AddressBook.Version[0] != '\0') {
            if (((ccode = ConnWriteF(client->user, "%s\r\n", server->AddressBook.Version)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Connection Pool\r\nCount: %010lu\r\nBytes: %010lu\r\nRequests: %010lu\r\nAllocs: %010lu\r\n", server->AddressBook.Memory.Count, server->AddressBook.Memory.Size, server->AddressBook.Memory.Pitches, server->AddressBook.Memory.Strikes)) != -1)) {
                ccode = ConnWriteF(client->user, "Threads\r\nProcesses: %010lu\r\nConnections: %010lu\r\n\r\n", server->AddressBook.ServerThreads, server->AddressBook.Connections);
            }
        } else {
            ccode = ConnWriteF(client->user, "%s Not Loaded\r\n\r\n", MSGSRV_AGENT_ADDRESSBOOK);
        }

        if (server->Alias.Version[0] != '\0') {
            if (((ccode = ConnWriteF(client->user, "%s\r\n", server->Alias.Version)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Connection Pool\r\nCount: %010lu\r\nBytes: %010lu\r\nRequests: %010lu\r\nAllocs: %010lu\r\n", server->Alias.Memory.Count, server->Alias.Memory.Size, server->Alias.Memory.Pitches, server->Alias.Memory.Strikes)) != -1)) {
                ccode = ConnWriteF(client->user, "Threads\r\nProcesses: %010lu\r\nConnections: %010lu\r\n\r\n", server->Alias.ServerThreads, server->Alias.Connections);
            }
        } else {
            ccode = ConnWriteF(client->user, "%s Not Loaded\r\n\r\n", MSGSRV_AGENT_ALIAS);
        }

        if (server->AntiSpam.Version[0] != '\0') {
            if (((ccode = ConnWriteF(client->user, "%s\r\n", server->AntiSpam.Version)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Connection Pool\r\nCount: %010lu\r\nBytes: %010lu\r\nRequests: %010lu\r\nAllocs: %010lu\r\n", server->AntiSpam.Memory.Count, server->AntiSpam.Memory.Size, server->AntiSpam.Memory.Pitches, server->AntiSpam.Memory.Strikes)) != -1)) {
                ccode = ConnWriteF(client->user, "Threads\r\nProcesses: %010lu\r\nConnections: %010lu\r\nIdle: %010lu\r\n\r\n", server->AntiSpam.ServerThreads, server->AntiSpam.Connections, server->AntiSpam.Idle);
            }
        } else {
            ccode = ConnWriteF(client->user, "%s Not Loaded\r\n\r\n", MSGSRV_AGENT_ANTISPAM);
        }

        if (server->AntiVirus.Version[0] != '\0') {
            if (((ccode = ConnWriteF(client->user, "%s\r\n", server->AntiVirus.Version)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Connection Pool\r\nCount: %010lu\r\nBytes: %010lu\r\nRequests: %010lu\r\nAllocs: %010lu\r\n", server->AntiVirus.Memory.Count, server->AntiVirus.Memory.Size, server->AntiVirus.Memory.Pitches, server->AntiVirus.Memory.Strikes)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Threads\r\nProcesses: %010lu\r\nConnections: %010lu\r\nIdle: %010lu\r\n", server->AntiVirus.ServerThreads, server->AntiVirus.Connections, server->AntiVirus.Idle)) != -1)) {
                ccode = ConnWriteF(client->user, "Messages\r\nScanned: %010lu\r\nAttachments\r\nScanned: %010lu\r\nBlocked: %010lu\r\nVirus\r\nInfections: %010lu\r\n\r\n", server->AntiVirus.MessagesScanned, server->AntiVirus.AttachmentsScanned, server->AntiVirus.AttachmentsBlocked, server->AntiVirus.VirusesFound);
            }
        } else {
            ccode = ConnWriteF(client->user, "%s Not Loaded\r\n\r\n", MSGSRV_AGENT_ANTIVIRUS);
        }

        if (server->Calendar.Version[0] != '\0') {
            if (((ccode = ConnWriteF(client->user, "%s\r\n", server->Calendar.Version)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Connection Pool\r\nCount: %010lu\r\nBytes: %010lu\r\nRequests: %010lu\r\nAllocs: %010lu\r\n", server->Calendar.Memory.Count, server->Calendar.Memory.Size, server->Calendar.Memory.Pitches, server->Calendar.Memory.Strikes)) != -1)) {
                ccode = ConnWriteF(client->user, "Threads\r\nProcesses: %010lu\r\nConnections: %010lu\r\nIdle: %010lu\r\n\r\n", server->Calendar.ServerThreads, server->Calendar.Connections, server->Calendar.Idle);
            }
        } else {
            ccode = ConnWriteF(client->user, "%s Not Loaded\r\n\r\n", MSGSRV_AGENT_ITIP);
        }

        if (server->ConnectionManager.Version[0] != '\0') {
            if (((ccode = ConnWriteF(client->user, "%s\r\n", server->ConnectionManager.Version)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Connection Pool\r\nCount: %010lu\r\nBytes: %010lu\r\nRequests: %010lu\r\nAllocs: %010lu\r\n", server->ConnectionManager.Memory.Count, server->ConnectionManager.Memory.Size, server->ConnectionManager.Memory.Pitches, server->ConnectionManager.Memory.Strikes)) != -1)) {
                ccode = ConnWriteF(client->user, "Threads\r\nProcesses: %010lu\r\nConnections: %010lu\r\n\r\n", server->ConnectionManager.ServerThreads, server->ConnectionManager.Connections);
            }
        } else {
            ccode = ConnWriteF(client->user, "%s Not Loaded\r\n\r\n", MSGSRV_AGENT_GATEKEEPER);
        }

        if (server->Finger.Version[0] != '\0') {
            if (((ccode = ConnWriteF(client->user, "%s\r\n", server->Finger.Version)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Connection Pool\r\nCount: %010lu\r\nBytes: %010lu\r\nRequests: %010lu\r\nAllocs: %010lu\r\n", server->Finger.Memory.Count, server->Finger.Memory.Size, server->Finger.Memory.Pitches, server->Finger.Memory.Strikes)) != -1)) {
                ccode = ConnWriteF(client->user, "Threads\r\nProcesses: %010lu\r\nConnections: %010lu\r\n\r\n", server->Finger.ServerThreads, server->Finger.Connections);
            }
        } else {
            ccode = ConnWriteF(client->user, "%s Not Loaded\r\n\r\n", MSGSRV_AGENT_FINGER);
        }

        if (server->IMAP.Version[0] != '\0') {
            if (((ccode = ConnWriteF(client->user, "%s\r\n", server->IMAP.Version)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Connection Pool\r\nCount: %010lu\r\nBytes: %010lu\r\nRequests: %010lu\r\nAllocs: %010lu\r\n", server->IMAP.Memory.Count, server->IMAP.Memory.Size, server->IMAP.Memory.Pitches, server->IMAP.Memory.Strikes)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Threads\r\nProcesses: %010lu\r\nConnections: %010lu\r\nIdle: %010lu\r\nConnections Limit: %010lu\r\n", server->IMAP.ServerThreads, server->IMAP.Connections, server->IMAP.Idle, server->IMAP.ConnectionLimit)) != -1)) {
                ccode = ConnWriteF(client->user, "Statistics\r\nTotal Sessions: %010lu\r\nBad Passwords: %010lu\r\n\r\n", server->IMAP.ConnectionsServiced, server->IMAP.BadPasswords);
            }
        } else {
            ccode = ConnWriteF(client->user, "%s Not Loaded\r\n\r\n", MSGSRV_AGENT_IMAP);
        }

        if (server->List.Version[0] != '\0') {
            if (((ccode = ConnWriteF(client->user, "%s\r\n", server->List.Version)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Connection Pool\r\nCount: %010lu\r\nBytes: %010lu\r\nRequests: %010lu\r\nAllocs: %010lu\r\n", server->List.Memory.Count, server->List.Memory.Size, server->List.Memory.Pitches, server->List.Memory.Strikes)) != -1)) {
                ccode = ConnWriteF(client->user, "Threads\r\nProcesses: %010lu\r\nConnections: %010lu\r\n\r\n", server->List.ServerThreads, server->List.Connections);
            }
        } else {
            ccode = ConnWriteF(client->user, "%s Not Loaded\r\n\r\n", MSGSRV_AGENT_LIST);
        }

        /* NMAPStatistics */
        if (server->NMAP.Version[0] != '\0') {
            if (((ccode = ConnWriteF(client->user, "%s\r\n", server->NMAP.Version)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Connection Pool\r\nCount: %010lu\r\nBytes: %010lu\r\nRequests: %010lu\r\nAllocs: %010lu\r\n", server->NMAP.Memory.Count, server->NMAP.Memory.Size, server->NMAP.Memory.Pitches, server->NMAP.Memory.Strikes)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Threads\r\nProcesses: %010lu\r\n", server->NMAP.ServerThreads)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Load\r\nMonitor: %010lu\r\nInterval: %010lu\r\nUtilization Low: %010lu\r\nUtilization High: %010lu\r\n", (server->NMAP.Monitor.Disabled != -1)? 0: 1, server->NMAP.Monitor.Interval, server->NMAP.Utilization.Low, server->NMAP.Utilization.High)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Bounce\r\nInterval: %010lu\r\nMaximum: %010lu\r\n", server->NMAP.Bounce.Interval, server->NMAP.Bounce.Maximum)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Queue\r\nConnections: %010lu\r\nServiced: %010lu\r\nThreshold: %010lu\r\nConcurrent: %010lu\r\nSequential: %010lu\r\n", server->NMAP.QueueThreads, server->NMAP.NMAP.Agents, server->NMAP.Queue.Threshold, server->NMAP.Queue.Concurrent, server->NMAP.Queue.Sequential)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Messages\r\nQueued: %010lu\r\nSPAM Discarded: %010lu\r\nStored Count: %010lu\r\nStored Recipients: %010lu\r\nStored KiloBytes: %010lu\r\nStore Failed: %010lu\r\nRemote Failed: %010lu\r\n", server->NMAP.Messages.Queued, server->NMAP.Messages.SPAMDiscarded, server->NMAP.Messages.Stored.Count, server->NMAP.Messages.Stored.Recipients, server->NMAP.Messages.Stored.KiloBytes, server->NMAP.Messages.Failed.Local, server->NMAP.Messages.Failed.Remote)) != -1)) {
                ccode = ConnWriteF(client->user, "NMAP\r\nConnections: %010lu\r\nIdle: %010lu\r\nRemote: %010lu\r\nServiced: %010lu\r\n\r\n", server->NMAP.Connections, server->NMAP.Idle, server->NMAP.NMAP.Connections, server->NMAP.NMAP.Serviced);
            }
        } else {
            ccode = ConnWriteF(client->user, "%s Not Loaded\r\n\r\n", MSGSRV_AGENT_STORE);
        }

        /* PlusPackStatistics */
        if (server->PlusPack.Version[0] != '\0') {
            if (((ccode = ConnWriteF(client->user, "%s\r\n", server->PlusPack.Version)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Connection Pool\r\nCount: %010lu\r\nBytes: %010lu\r\nRequests: %010lu\r\nAllocs: %010lu\r\n", server->PlusPack.Memory.Count, server->PlusPack.Memory.Size, server->PlusPack.Memory.Pitches, server->PlusPack.Memory.Strikes)) != -1)) {
                ccode = ConnWriteF(client->user, "Threads\r\nProcesses: %010lu\r\nConnections: %010lu\r\n\r\n", server->PlusPack.ServerThreads, server->PlusPack.Connections);
            }
        } else {
            ccode = ConnWriteF(client->user, "%s Not Loaded\r\n\r\n", PLUSPACK_AGENT);
        }

        /* POP3Statistics */
        if (server->POP3.Version[0] != '\0') {
            if (((ccode = ConnWriteF(client->user, "%s\r\n", server->POP3.Version)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Connection Pool\r\nCount: %010lu\r\nBytes: %010lu\r\nRequests: %010lu\r\nAllocs: %010lu\r\n", server->POP3.Memory.Count, server->POP3.Memory.Size, server->POP3.Memory.Pitches, server->POP3.Memory.Strikes)) != -1)) {
                ccode = ConnWriteF(client->user, "Threads\r\nProcesses: %010lu\r\nConnections: %010lu\r\nIdle: %010lu\r\n\r\n", server->POP3.ServerThreads, server->POP3.Connections, server->POP3.Idle);
            }
        } else {
            ccode = ConnWriteF(client->user, "%s Not Loaded\r\n\r\n", MSGSRV_AGENT_POP);
        }

        /* ProxyStatistics */
        if (server->Proxy.Version[0] != '\0') {
            if (((ccode = ConnWriteF(client->user, "%s\r\n", server->Proxy.Version)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Connection Pool\r\nCount: %010lu\r\nBytes: %010lu\r\nRequests: %010lu\r\nAllocs: %010lu\r\n", server->Proxy.Memory.Count, server->Proxy.Memory.Size, server->Proxy.Memory.Pitches, server->Proxy.Memory.Strikes)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Threads\r\nProcesses: %010lu\r\nConnections: %010lu\r\nIdle: %010lu\r\n", server->Proxy.ServerThreads, server->Proxy.Connections, server->Proxy.Idle)) != -1)) {
                ccode = ConnWriteF(client->user, "Statistics\r\nTotal Sessions: %010lu\r\nBad Passwords: %010lu\r\nMessages Proxied: %010lu\r\nKiloBytes Proxied: %010lu\r\n\r\n", server->Proxy.ConnectionsServiced, server->Proxy.BadPasswords, server->Proxy.Messages, server->Proxy.KiloBytes);
            }
        } else {
            ccode = ConnWriteF(client->user, "%s Not Loaded\r\n\r\n", MSGSRV_AGENT_PROXY);
        }

        /* RulesServerStatistics */
        if (server->Rules.Version[0] != '\0') {
            if (((ccode = ConnWriteF(client->user, "%s\r\n", server->Rules.Version)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Connection Pool\r\nCount: %010lu\r\nBytes: %010lu\r\nRequests: %010lu\r\nAllocs: %010lu\r\n", server->Rules.Memory.Count, server->Rules.Memory.Size, server->Rules.Memory.Pitches, server->Rules.Memory.Strikes)) != -1)) {
                ccode = ConnWriteF(client->user, "Threads\r\nProcesses: %010lu\r\nConnections: %010lu\r\nIdle: %010lu\r\n\r\n", server->Rules.ServerThreads, server->Rules.Connections, server->Rules.Idle);
            }
        } else {
            ccode = ConnWriteF(client->user, "%s Not Loaded\r\n\r\n", MSGSRV_AGENT_RULESRV);
        }

        /* SMTPStatistics */
        if (server->SMTP.Version[0] != '\0') {
            if (((ccode = ConnWriteF(client->user, "%s\r\n", server->SMTP.Version)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Connection Pool\r\nCount: %010lu\r\nBytes: %010lu\r\nRequests: %010lu\r\nAllocs: %010lu\r\n", server->SMTP.Memory.Count, server->SMTP.Memory.Size, server->SMTP.Memory.Pitches, server->SMTP.Memory.Strikes)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Threads\r\nProcesses: %010lu\r\nConnections: %010lu\r\nIdle: %010lu\r\nLimit: %010lu\r\n", server->SMTP.ServerThreads, server->SMTP.Connections, server->SMTP.Idle, server->SMTP.ConnectionLimit)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Blocked\r\nAddress: %010lu\r\nMAPS: %010lu\r\nNoDNSEntry: %010lu\r\nRouting: %010lu\r\nRTS SPAM: %010lu\r\n", server->SMTP.Blocked.Address, server->SMTP.Blocked.MAPS, server->SMTP.Blocked.NoDNSEntry, server->SMTP.Blocked.Routing, server->SMTP.Blocked.Queue)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Incoming\r\nSessions: %010lu\r\nServiced: %010lu\r\nBad Passwords: %010lu\r\nRemote Messages: %010lu\r\nRemote Recipients: %010lu\r\nRemote KiloBytes: %010lu\r\nLocal Messages: %010lu\r\nLocal Recipients: %010lu\r\nLocal KiloBytes: %010lu\r\n", server->SMTP.Connections, server->SMTP.Servers.InBound.Serviced, server->SMTP.BadPasswords, server->SMTP.Remote.Messages.Received, server->SMTP.Remote.Recipients.Received, server->SMTP.Remote.KiloBytes.Received, server->SMTP.Local.Count, server->SMTP.Local.Recipients, server->SMTP.Local.KiloBytes)) != -1) 
                    && ((ccode = ConnWriteF(client->user, "Queue\r\nProcessing: %010lu\r\nDelivering: %010lu\r\n", server->SMTP.QueueThreads, server->SMTP.NMAP.Connections)) != -1)) {
                ccode = ConnWriteF(client->user, "Outgoing\r\nSessions: %010lu\r\nServiced: %010lu\r\nStored Messages: %010lu\r\nStored Recipients: %010lu\r\nStored KiloBytes: %010lu\r\n", server->SMTP.Servers.OutBound.Connections, server->SMTP.Servers.OutBound.Serviced, server->SMTP.Remote.Messages.Stored, server->SMTP.Remote.Recipients.Stored, server->SMTP.Remote.KiloBytes.Stored);
            }
        } else {
            ccode = ConnWriteF(client->user, "%s Not Loaded\r\n\r\n", MSGSRV_AGENT_SMTP);
        }

        if (ccode != -1) {
            ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
        }
    }

    if (server) {
        MemFree(server);
    }

    return(ccode);
}

static int 
DMCCommandServer(void *param)
{
    int ccode;
    unsigned long i;
    unsigned char *ptr;
    BOOL result;
    MDBValueStruct *v;
    ManagedServer *server;
    DMCClient *client = param;

    ccode = -1;
    switch (client->state) {
        case CLIENT_STATE_CLOSING:
        case CLIENT_STATE_FRESH:
        case CLIENT_STATE_AGENT: {
            ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
            break;
        }

        case CLIENT_STATE_AUTHENTICATED: {
            if (client->buffer[6] == ' ') {
                if ((client->buffer[7] == '"') 
                        && ((ptr = strchr(client->buffer + 8, '"')) != NULL)) {
                    *ptr = '\0';
                    server = NULL;
                } else {
                    ccode = ConnWrite(client->user, DMCMSG2101BADPARAMS, sizeof(DMCMSG2101BADPARAMS) - 1);
                    break;
                }

                for (i = 0; i < DMC.server.managed.count; i++) {
                    if (XplStrCaseCmp(DMC.server.managed.list[i].identity + DMC.server.managed.list[i].rdnOffset, client->buffer + 8) == 0) {
                        server = &(DMC.server.managed.list[i]);
                        break;
                    }
                }

                result = FALSE;
                if (server) {
                    if (server->flags & SERVER_FLAG_REGISTERED) {
                        v = MDBCreateValueStruct(DMC.handles.directory, NULL);
                    } else {
                        ccode = ConnWrite(client->user, DMCMSG1104SERVER_DEREGISTERED, sizeof(DMCMSG1104SERVER_DEREGISTERED) - 1);
                        break;
                    }

                    if (v) {
                        if (MDBReadDN(server->identity, MSGSRV_A_MANAGER, v)) {
                            for (i = 0; i < v->Used; i++) {
                                if (XplStrCaseCmp(client->identity, v->Value[i]) == 0) {
                                    result = TRUE;
                                    break;
                                }
                            }
                        }

                        MDBDestroyValueStruct(v);
                    } else {
                        ccode = ConnWrite(client->user, DMCMSG2000NOMEMORY, sizeof(DMCMSG2000NOMEMORY) - 1);
                        break;
                    }
                } else {
                    ccode = ConnWrite(client->user, DMCMSG2101BADPARAMS, sizeof(DMCMSG2101BADPARAMS) - 1);
                    break;
                }

                if (result) {
                    if (server->flags & SERVER_FLAG_LOCAL) {
                        client->server = server;
                        client->state = CLIENT_STATE_SERVER;
                        ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
                        break;
                    }

                    memset(&(client->resource->socketAddress), 0, sizeof(client->resource->socketAddress));
                    client->resource->socketAddress.sin_family = AF_INET;
                    client->resource->socketAddress.sin_addr.s_addr = server->address.clear.sin_addr.s_addr;
                    if ((client->resource->socketAddress.sin_port = server->address.clear.sin_port) != (unsigned short)-1) {
                        ccode = ConnConnectEx(client->resource, NULL, 0, NULL, client->user->trace.destination);
                    } else if (DMC.client.ssl.enable && ((client->resource->socketAddress.sin_port = server->address.ssl.sin_port) != (unsigned short)-1)) {
                        ccode = ConnConnectEx(client->resource, NULL, 0, DMC.client.ssl.context, client->user->trace.destination);
                    } else {
                        ccode = ConnWrite(client->user, DMCMSG2105PORT_DISABLED, sizeof(DMCMSG2105PORT_DISABLED) - 1);
                        break;
                    }

                    if ((ccode != -1) 
                            && ((ccode = ConnReadAnswer(client->resource, client->buffer, CONN_BUFSIZE) != -1)) 
                            && ((ccode = ConnWriteF(client->resource,  "LOGIN %s %s\r\n", client->identity + client->rdnOffset, client->credential)) != -1) 
                            && ((ccode = ConnFlush(client->resource)) != -1) 
                            && ((ccode = ConnReadAnswer(client->resource, client->buffer, CONN_BUFSIZE)) != -1) 
                            && (strncmp(client->buffer, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 3) == 0) 
                            && ((ccode = ConnWriteF(client->resource,  "SERVER \"%s\"\r\n", server->identity + server->rdnOffset)) != -1) 
                            && ((ccode = ConnFlush(client->resource)) != -1) 
                            && ((ccode = ConnReadLine(client->resource, client->buffer, CONN_BUFSIZE)) != -1) 
                            && (strncmp(client->buffer, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 3) == 0)) {
                        client->server = server;
                        client->state = CLIENT_STATE_SERVER;

                        BridgeConnection(client);

                        if ((toupper(client->buffer[0]) == 'Q') && (XplStrCaseCmp(client->buffer, "QUIT") == 0)) {
                            client->flags &= ~CLIENT_FLAG_CONNECTED;
                        }

                        break;
                    }

                    ConnWrite(client->resource,  "QUIT\r\n", 6);
                    ConnClose(client->resource, 0);
                    client->resource = NULL;

                    ccode = ConnWrite(client->user, DMCMSG2106CONNECT_FAILED, sizeof(DMCMSG2106CONNECT_FAILED) - 1);
                    break;
                }

                ccode = ConnWrite(client->user, DMCMSG2001NOT_MANAGER, sizeof(DMCMSG2001NOT_MANAGER) - 1);
                break;
            }

            ccode = ConnWriteF(client->user, DMCMSG1001RESPONSES_COMMING, DMC.server.managed.count);
            for (i = 0; (ccode != -1) && (i < DMC.server.managed.count); i++) {
                if (DMC.server.managed.list[i].flags & SERVER_FLAG_REGISTERED) {
                    ccode = ConnWriteF(client->user, "\"%s\"\r\n", DMC.server.managed.list[i].identity + DMC.server.managed.list[i].rdnOffset);
                }
            }

            if (ccode != -1) {
                ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
            } else {
                ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
            }

            break;
        }

        case CLIENT_STATE_SERVER: {
            ccode = ConnWrite(client->user, DMCMSG2103RESET, sizeof(DMCMSG2103RESET) - 1);
            break;
        }

        default: {
            ccode = ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1);
            break;
        }
    }

    return(ccode);
}

static int 
DMCCommandSet(void *param)
{
    int ccode;
    unsigned char *ptr;
    unsigned long index;
    unsigned long count;
    ManagedAgent *agent;
    DMCClient *client = param;

    if (client->state == CLIENT_STATE_AGENT) {
        agent = client->agent;
    } else {
        return(ConnWrite(client->user, DMCMSG2102BADSTATE, sizeof(DMCMSG2102BADSTATE) - 1));
    }

    if (agent->flags & AGENT_FLAG_REGISTERED) {
        if ((client->buffer[3] == ' ') 
                && (client->buffer[4] == '"') 
                && ((ptr = strchr(client->buffer + 5, '"')) != NULL) 
                && (ptr[1] == ' ') 
                && (isdigit(ptr[2]))) {
            *ptr = '\0';
            ptr += 2;
        } else {
            return(ConnWrite(client->user, DMCMSG2101BADPARAMS, sizeof(DMCMSG2101BADPARAMS) - 1));
        }

        index = -1;
        for (count = 0; count < agent->variable.count; count++) {
            if (XplStrCaseCmp(agent->variable.list[count].name, client->buffer + 5) == 0) {
                index = count;
                break;
            }
        }

        if ((index == count) && ((count = (unsigned long)atol(ptr)) > 0)) {
            if (agent->variable.list[index].flags & VARIABLE_FLAG_WRITEABLE) {
                if (((ccode = ConnWriteF(client->resource, "WRITE %d %d\r\n", index, count)) != -1) 
                        && ((ccode = ConnFlush(client->resource)) != -1) 
                        && ((ccode = ConnReadLine(client->resource, client->buffer, CONN_BUFSIZE)) != -1) 
                        && (strcmp(client->buffer, DMCMSG1003SEND_VALUE) == 0) 
                        && ((ccode = ConnWriteF(client->user, DMCMSG1003SEND_BYTES, count)) != -1) 
                        && ((ccode = ConnFlush(client->user)) != -1) 
                        && ((ccode = ConnReadToConn(client->user, client->resource, count)) != -1) 
                        && ((ccode = ConnFlush(client->resource)) != -1)) {
                    if (((ccode = ConnReadLine(client->resource, client->buffer, CONN_BUFSIZE)) != -1) 
                            && (strcmp(client->buffer, DMCMSG1000OK) == 0)) {
                        ccode = ConnWrite(client->user, DMCMSG1000OK, sizeof(DMCMSG1000OK) - 1);
                    }
                }

                if (ccode == -1) {
                    ConnWrite(client->user, DMCMSG2003CONNECTION_CLOSED, sizeof(DMCMSG2003CONNECTION_CLOSED) - 1);

                    client->flags &= ~CLIENT_FLAG_CONNECTED;
                }
            } else {
                ccode = ConnWrite(client->user, DMCMSG2104NOT_WRITEABLE, sizeof(DMCMSG2104NOT_WRITEABLE) - 1);
            }
        } else {
            ccode = ConnWrite(client->user, DMCMSG2101BADPARAMS, sizeof(DMCMSG2101BADPARAMS) - 1);
        }
    } else {
        ccode = ConnWrite(client->user, DMCMSG1104AGENT_DEREGISTERED, sizeof(DMCMSG1104AGENT_DEREGISTERED) - 1);
    }

    return(ccode);;
}

static void 
HandleConnection(void *param)
{
    int i;
    int ccode;
    long threadNumber = (long)param;
    BOOL result;
    time_t sleep = time(NULL);
    time_t wokeup;
    DMCClient *client;

    client = ClientAlloc();
    if (!client) {
        XplSafeDecrement(DMC.client.worker.active);

        return;
    }

    do {
        XplRenameThread(XplGetThreadID(), "DMC Worker");

        XplSafeIncrement(DMC.client.worker.idle);

        XplWaitOnLocalSemaphore(DMC.client.worker.todo);

        XplSafeDecrement(DMC.client.worker.idle);

        wokeup = time(NULL);

        XplWaitOnLocalSemaphore(DMC.client.semaphore);

        client->user = DMC.client.worker.tail;
        if (client->user) {
            DMC.client.worker.tail = client->user->queue.previous;
            if (DMC.client.worker.tail) {
                DMC.client.worker.tail->queue.next = NULL;
            } else {
                DMC.client.worker.head = NULL;
            }
        }

        XplSignalLocalSemaphore(DMC.client.semaphore);

        if (client->user) {
            if (!client->user->ssl.enable) {
                client->flags &= ~CLIENT_FLAG_CONNECTED;

                XplRWReadLockAcquire(&(DMC.lock));
                for (i = 0; i < DMC.trusted.count; i++) {
                    if (client->user->socketAddress.sin_addr.s_addr == DMC.trusted.hosts[i]) {
                        client->flags |= CLIENT_FLAG_CONNECTED;
                        break;
                    }
                }
                XplRWReadLockRelease(&(DMC.lock));

                if (!(client->flags & CLIENT_FLAG_CONNECTED) && XplIsLocalIPAddress(client->user->socketAddress.sin_addr.s_addr)) {
                    client->flags |= CLIENT_FLAG_CONNECTED;
                }
            } else {
                client->flags |= CLIENT_FLAG_CONNECTED;
            }

            ccode = -1;
            if (client->flags & CLIENT_FLAG_CONNECTED) {
                result = ConnNegotiate(client->user, DMC.server.ssl.context);
                if (result) {
                    if (client->user->ssl.enable == FALSE) {
                        LoggerEvent(DMC.handles.logging, LOGGER_SUBSYSTEM_AUTH, LOGGER_EVENT_CONNECTION, LOG_INFO, 0, NULL, NULL, XplHostToLittle(client->user->socketAddress.sin_addr.s_addr), 0, NULL, 0);
                    } else {
                        LoggerEvent(DMC.handles.logging, LOGGER_SUBSYSTEM_AUTH, LOGGER_EVENT_SSL_CONNECTION, LOG_INFO, 0, NULL, NULL, XplHostToLittle(client->user->socketAddress.sin_addr.s_addr), 0, NULL, 0);
                    }

                    if ((ccode = ConnWriteF(client->user, "1000 \"%s\" Bongo Manager\r\n", DMC.rdn)) != -1) {
                        ccode = ConnFlush(client->user);
                    }
                }
            }

            if (ccode == -1) {
                ConnClose(client->user, 1);
                ConnFree(client->user);
                client->user = NULL;
            }
        }

        while ((ccode != -1) && (DMC.state == DMC_RUNNING) && (client->flags & CLIENT_FLAG_CONNECTED)) {
            ccode = ConnReadAnswer(client->user, client->buffer, CONN_BUFSIZE);

            if ((ccode != -1) && (ccode < CONN_BUFSIZE)) {

#if 0
                XplConsolePrintf("[%d.%d.%d.%d] Got command: %s %s\r\n", 
                        client->user->socketAddress.sin_addr.s_net, 
                        client->user->socketAddress.sin_addr.s_host, 
                        client->user->socketAddress.sin_addr.s_lh, 
                        client->user->socketAddress.sin_addr.s_impno, 
                        client->buffer, 
                        (client->user->ssl.enable)? "(Secure)": "");
#endif

                client->command = ProtocolCommandTreeSearch(&DMC.commands, client->buffer);
                if (client->command) {
                    ccode = client->command->handler(client);
                } else {
                    ccode = ConnWrite(client->user, DMCMSG2100BADCOMMAND, sizeof(DMCMSG2100BADCOMMAND) - 1);

                    LoggerEvent(DMC.handles.logging, LOGGER_SUBSYSTEM_UNHANDLED, LOGGER_EVENT_UNHANDLED_REQUEST, LOG_INFO, 0, client->identity, client->buffer, XplHostToLittle(client->user->socketAddress.sin_addr.s_addr), 0, NULL, 0);
                }
            } else {
                ccode = ConnWrite(client->user, DMCMSG2100BADCOMMAND, sizeof(DMCMSG2100BADCOMMAND) - 1);

                LoggerEvent(DMC.handles.logging, LOGGER_SUBSYSTEM_UNHANDLED, LOGGER_EVENT_UNHANDLED_REQUEST, LOG_INFO, 0, client->identity, client->buffer, XplHostToLittle(client->user->socketAddress.sin_addr.s_addr), 0, NULL, 0);
            }

            SEND_PROMPT(client);

            ConnFlush(client->user);

            continue;
        }

        if (client->resource) {
            ConnWrite(client->resource, "QUIT\r\n", 6);
            ConnClose(client->resource, 0);
            client->resource = NULL;
        }

        if (client->user) {
            ConnClose(client->user, 0);
            client->user = NULL;
        }

        /* Live or die? */
        if (threadNumber == (long) (XplSafeRead(DMC.client.worker.active))) {
            if ((wokeup - sleep) > DMC.client.sleepTime) {
                break;
            }
        }

        sleep = time(NULL);

        ClientAllocCB(client, NULL);
    } while (DMC.state == DMC_RUNNING);

    ClientFree(client);

    XplSafeDecrement(DMC.client.worker.active);

    XplExitThread(TSR_THREAD, 0);

    return;
}

static int 
ServerSocketInit (void)
{
    int bind_errno;
    DMC.server.conn = ConnAlloc(FALSE);
 
    if (DMC.server.conn) {
        DMC.server.conn->socketAddress.sin_family = AF_INET;
        DMC.server.conn->socketAddress.sin_port = htons(DMC.server.port);
        DMC.server.conn->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();	

	/* Get root privs back for the binds.  It's ok if this fails - 
	 * the user might not need to be root to bind to the port */
	XplSetEffectiveUserId(0);
	
        DMC.server.conn->socket = ConnServerSocket(DMC.server.conn, 2048);
        bind_errno = errno;

	if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
	    LoggerEvent(DMC.handles.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_PRIV_FAILURE, LOG_ERROR, 0, MsgGetUnprivilegedUser(), NULL, 0, 0, NULL, 0);
	    XplConsolePrintf("bongodmc: Could not drop to unprivileged user '%s'\n", MsgGetUnprivilegedUser());
	    return -1;
	}

	if (DMC.server.conn->socket == -1) {
	    int ret = DMC.server.conn->socket;
	    LoggerEvent(DMC.handles.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_BIND_FAILURE, LOG_ERROR, 0, "DMC ", NULL, DMC.server.port, 0, NULL, 0);
	    XplConsolePrintf("bongodmc: Could not bind to port %d: (%d) %s\n",
                             DMC.server.port,
                             bind_errno, strerror(bind_errno));
			     
	    ConnFree(DMC.server.conn);
	    return ret;
	}
    } else {
	LoggerEvent(DMC.handles.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_CREATE_SOCKET_FAILED, LOG_ERROR, 0, "", NULL, __LINE__, 0, NULL, 0);
	XplConsolePrintf("bongodmc: Could not allocate connection.\n");
	return -1;
    }

    return 0;
}

static int
ServerSocketSSLInit(void)
{
    DMC.server.ssl.conn = ConnAlloc(FALSE);
    if (DMC.server.ssl.conn) {
	DMC.server.ssl.conn->socketAddress.sin_family = AF_INET;
	DMC.server.ssl.conn->socketAddress.sin_port = htons(DMC.server.ssl.port);
	DMC.server.ssl.conn->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();
	
	/* Get root privs back for the binds.  It's ok if this fails - 
	 * the user might not need to be root to bind to the port */
	XplSetEffectiveUserId(0);

	DMC.server.ssl.conn->socket = ConnServerSocket(DMC.server.ssl.conn, 2048);

	if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
	    LoggerEvent(DMC.handles.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_PRIV_FAILURE, LOG_ERROR, 0, MsgGetUnprivilegedUser(), NULL, 0, 0, NULL, 0);
	    XplConsolePrintf("bongodmc: Could not drop to unprivileged user '%s'\n", MsgGetUnprivilegedUser());
	    return -1;
	}
	
	if (DMC.server.ssl.conn->socket == -1) {
	    int ret = DMC.server.ssl.conn->socket;
	    LoggerEvent(DMC.handles.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_BIND_FAILURE, LOG_ERROR, 0, "DMC-SSL ", NULL, DMC.server.ssl.port, 0, NULL, 0);
	    XplConsolePrintf("bongodmc: Could not bind to SSL port %d\n", DMC.server.ssl.port);
	    ConnFree(DMC.server.conn);
	    ConnFree(DMC.server.ssl.conn);
	    
	    return ret;
	}
    } else {
	LoggerEvent(DMC.handles.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_CREATE_SOCKET_FAILED, LOG_ERROR, 0, "", NULL, __LINE__, 0, NULL, 0);
	XplConsolePrintf("bongodmc: Could not allocate SSL connection.\n");
	ConnFree(DMC.server.conn);
	return -1;
    }

    return 0;
}

static void 
DMCServer(void *ignored)
{
    int ccode;
    unsigned long i;
    unsigned long j;
    unsigned long k;
    XplThreadID id;
    Connection *conn;
    ManagedServer *server;
    ManagedAgent *agent;

    XplSafeIncrement(DMC.server.active);

    XplRenameThread(XplGetThreadID(), "DMC Server");

    DMC.state = DMC_RUNNING;

    while (DMC.state < DMC_STOPPING) {
        if (ConnAccept(DMC.server.conn, &conn) != -1) {
            if (DMC.state < DMC_STOPPING) {
                conn->ssl.enable = FALSE;

                QUEUE_WORK_TO_DO(conn, id, ccode);

                if (!ccode) {
                    XplSignalLocalSemaphore(DMC.client.worker.todo);

                    continue;
                }
            } else if (DMC.stopped) {
                ccode = DMC_RECEIVER_DISABLED;
            } else {
                ccode = DMC_RECEIVER_SHUTTING_DOWN;
            }

            switch(ccode) {
                case DMC_RECEIVER_SHUTTING_DOWN: {
                    ConnSend(conn, DMCMSG2004SERVER_DOWN, sizeof(DMCMSG2004SERVER_DOWN) - 1);
                    break;
                }

                case DMC_RECEIVER_DISABLED: {
                    ConnSend(conn, DMCMSG2006SERVER_NO_ACCEPT, sizeof(DMCMSG2006SERVER_NO_ACCEPT) - 1);
                    break;
                }

                case DMC_RECEIVER_CONNECTION_LIMIT: {
                    ConnSend(conn, DMCMSG2005SERVER_TOO_MANY, sizeof(DMCMSG2005SERVER_TOO_MANY) - 1);
                    break;
                }

                case DMC_RECEIVER_OUT_OF_MEMORY: {
                    ConnSend(conn, DMCMSG2000NOMEMORY, sizeof(DMCMSG2000NOMEMORY) - 1);
                    break;
                }

                default: {
                    break;
                }
            }

            ConnClose(conn, 0);

            ConnFree(conn);
            conn = NULL;

            continue;
        }

        switch (errno) {
            case ECONNABORTED:
#ifdef EPROTO
            case EPROTO:
#endif
            case EINTR: {
                if (DMC.state < DMC_STOPPING) {
                    LoggerEvent(DMC.handles.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);
                }

                continue;
            }
#ifdef EWOULDBLOCK
            case EWOULDBLOCK: {
                XplDelay(300);
                continue;
            }
#endif
            default: {
                if (DMC.state < DMC_STOPPING) {
                    XplConsolePrintf("DMC: Exiting after an accept() failure; error %d\n", errno);

                    LoggerEvent(DMC.handles.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);

                    DMC.state = DMC_STOPPING;
                }

                break;
            }
        }

        break;
    }

    /* Shutting down */
    DMC.state = DMC_STOPPING;

    id = XplSetThreadGroupID(DMC.id.group);

#if VERBOSE
    XplConsolePrintf("DMC: Closing server sockets\r\n");
#endif

    if (DMC.server.conn) {
        ConnClose(DMC.server.conn, 1);
        DMC.server.conn = NULL;
    }

    if (DMC.server.ssl.enable) {
        DMC.server.ssl.enable = FALSE;

        if (DMC.server.ssl.conn) {
            ConnClose(DMC.server.ssl.conn, 1);
            DMC.server.ssl.conn = NULL;
        }

        if (DMC.server.ssl.context) {
            ConnSSLContextFree(DMC.server.ssl.context);
            DMC.server.ssl.context = NULL;
        }
    }

    ConnCloseAll(1);

    /* Wait for the our siblings to leave quietly. */
    for (ccode = 0; (XplSafeRead(DMC.server.active) > 1) && (ccode < 60); ccode++) {
        XplDelay(1000);
    }

    if (XplSafeRead(DMC.server.active) > 1) {
        XplConsolePrintf("DMC: %d server threads outstanding; attempting forceful unload.\r\n", XplSafeRead(DMC.server.active) - 1);
    }

#if VERBOSE
    XplConsolePrintf("DMC: Shutting down %d client threads\r\n", XplSafeRead(DMC.client.worker.active));
#endif

    XplWaitOnLocalSemaphore(DMC.client.semaphore);

    ccode = XplSafeRead(DMC.client.worker.idle);
    while (ccode--) {
        XplSignalLocalSemaphore(DMC.client.worker.todo);
    }

    XplSignalLocalSemaphore(DMC.client.semaphore);

    for (ccode = 0; XplSafeRead(DMC.client.worker.active) && (ccode < 60); ccode++) {
        XplDelay(1000);
    }

    if (XplSafeRead(DMC.client.worker.active)) {
        XplConsolePrintf("DMC: %d threads outstanding; attempting forceful unload.\r\n", XplSafeRead(DMC.client.worker.active));
    }

    /*    Cleanup managed servers    */
    for (i = 0; i < DMC.server.managed.count; i++) {
        server = &(DMC.server.managed.list[i]);

        for (j = 0; j < server->agents.count; j++) {
            agent = &(server->agents.list[j]);
            for (k = 0; k < agent->variable.count; k++) {
                MemFree(agent->variable.list[k].name);
            }

            if (agent->variable.list) {
                MemFree(agent->variable.list);
                agent->variable.list = NULL;
            }

            agent->variable.count = 0;

            for (k = 0; k < agent->command.count; k++) {
                MemFree(agent->command.name[k]);
            }

            if (agent->command.name) {
                MemFree(agent->command.name);
                agent->command.name = NULL;
            }

            agent->command.count = 0;
        }

        if (server->agents.list) {
            MemFree(server->agents.list);
            server->agents.list = NULL;
        }

        server->agents.count = 0;
    }

    if (DMC.server.managed.list) {
        MemFree(DMC.server.managed.list);
    }

    DMC.server.managed.count = 0;

    if (DMC.trusted.hosts) {
        MemFree(DMC.trusted.hosts);
        DMC.trusted.hosts = NULL;
    }

    DMC.trusted.count = 0;

    XPLCryptoLockDestroy();    

    LoggerClose(DMC.handles.logging);
    DMC.handles.logging = NULL;

    MsgShutdown();

//  MDBShutdown();

    CONN_TRACE_SHUTDOWN();
    ConnShutdown();

    MemPrivatePoolFree(DMC.client.pool);
    MemoryManagerClose(MSGSRV_AGENT_SMTP);

#if VERBOSE
    XplConsolePrintf("DMC: Shutdown complete.\n");    
#endif

    XplSignalLocalSemaphore(DMC.sem.main);
    XplWaitOnLocalSemaphore(DMC.sem.shutdown);

    XplCloseLocalSemaphore(DMC.sem.shutdown);
    XplCloseLocalSemaphore(DMC.sem.main);

    XplSetThreadGroupID(id);

    return;
}

static void 
DMCSSLServer(void *ignored)
{
    int ccode;
    XplThreadID id;
    Connection *conn;

    XplSignalBlock();

    XplRenameThread(XplGetThreadID(), "DMC SSL Server");

    while (DMC.state < DMC_STOPPING) {
        if (ConnAccept(DMC.server.ssl.conn, &conn) != -1) {
            if ((DMC.state < DMC_STOPPING) && !DMC.stopped) {
                conn->ssl.enable = TRUE;

              QUEUE_WORK_TO_DO(conn, id, ccode);

                if (!ccode) {
                    XplSignalLocalSemaphore(DMC.client.worker.todo);

                    continue;
                }
            } else if (DMC.stopped) {
                ccode = DMC_RECEIVER_DISABLED;
            } else {
                ccode = DMC_RECEIVER_SHUTTING_DOWN;
            }

/* fixme - send appropriate responses */
#if 0
if (SSL_accept(client->CSSL) == 1) {
XPLIPWriteSSL(client->CSSL, "-ERR server error\r\n", 19);
}
SSL_free(client->CSSL);
client->CSSL = NULL;
#endif

            switch(ccode) {
                case DMC_RECEIVER_SHUTTING_DOWN: {
                    ConnSend(conn, DMCMSG2004SERVER_DOWN, sizeof(DMCMSG2004SERVER_DOWN) - 1);
                    break;
                }

                case DMC_RECEIVER_DISABLED: {
                    ConnSend(conn, DMCMSG2006SERVER_NO_ACCEPT, sizeof(DMCMSG2006SERVER_NO_ACCEPT) - 1);
                    break;
                }

                case DMC_RECEIVER_CONNECTION_LIMIT: {
                    ConnSend(conn, DMCMSG2005SERVER_TOO_MANY, sizeof(DMCMSG2005SERVER_TOO_MANY) - 1);
                    break;
                }

                case DMC_RECEIVER_OUT_OF_MEMORY: {
                    ConnSend(conn, DMCMSG2000NOMEMORY, sizeof(DMCMSG2000NOMEMORY) - 1);
                    break;
                }

                default: {
                    break;
                }
            }

            ConnClose(conn, 0);

            ConnFree(conn);
            conn = NULL;

            continue;
        }

        switch (errno) {
            case ECONNABORTED:
#ifdef EPROTO
            case EPROTO:
#endif
            case EINTR: {
                LoggerEvent(DMC.handles.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);

                continue;
            }

            default: {
#if VERBOSE
                XplConsolePrintf("DMCD: Exiting after an accept() failure; error %d\n", errno);
#endif

                LoggerEvent(DMC.handles.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);

                DMC.state = DMC_STOPPING;

                break;
            }
        }

        break;
    }

    id = XplSetThreadGroupID(DMC.id.group);

    DMC.server.ssl.enable = FALSE;

    if (DMC.server.conn) {
        ConnClose(DMC.server.conn, 1);
        DMC.server.conn = NULL;
    }

    if (DMC.server.ssl.conn) {
        ConnClose(DMC.server.ssl.conn, 1);
        DMC.server.ssl.conn = NULL;
    }

    if (DMC.server.ssl.context) {
        ConnSSLContextFree(DMC.server.ssl.context);
        DMC.server.ssl.context = NULL;
    }

    XplSetThreadGroupID(id);

    XplSafeDecrement(DMC.server.active);

#if VERBOSE
    XplConsolePrintf("\rDMCD: SSL Server shutdown.\r\n");
#endif

    return;
}

static BOOL 
DMCProcessAgentList(
        MDBValueStruct *List,
        DMCAgent **Head,
        DMCAgent **Tail,
        MDBValueStruct *V)
{
    unsigned long i;
    unsigned long j;
    BOOL result = TRUE;
    DMCAgent *agent;

    for (i = 0; i < List->Used; i++) {
        if (MDBRead(List->Value[i], MSGSRV_A_MODULE_NAME, V)) {
            DMC.agents.count++;
            agent = (DMCAgent *)MemMalloc(sizeof(DMCAgent));
            if (agent) {
                agent->flags = 0;
                agent->priority = DMC_AGENT_DEFAULT_PRIORITY;
                agent->data = NULL;
                agent->next = NULL;
                agent->load = 0;
                if ((agent->previous = *Tail) != NULL) {
                    (*Tail)->next = agent;
                } else {
                    *Head = agent;
                }
                *Tail = agent;
                strcpy(agent->name, V->Value[0]);
                MDBFreeValues(V);
                if (XplStrCaseCmp(agent->name, MSGSRV_NLM_STORE) == 0) {
                    agent->flags |= DMC_FLAG_MODULE_NMAP;
                    agent->priority = DMC_AGENT_NMAP_PRIORITY;
                } else if (XplStrCaseCmp(agent->name, MSGSRV_NLM_QUEUE) == 0) {
                    agent->flags |= DMC_FLAG_MODULE_QUEUE;
                    agent->priority = DMC_AGENT_QUEUE_PRIORITY;
                } else if (XplStrCaseCmp(agent->name, MSGSRV_NLM_SMTP) == 0) {
                    agent->flags |= DMC_FLAG_MODULE_SMTP;
                    agent->priority = DMC_AGENT_SMTP_PRIORITY;
                }

                if (DMCAgentPrep(agent) == TRUE) {
                    if (MDBRead(List->Value[i], MSGSRV_A_CONFIGURATION, V)) {
                        for (j = 0; j < V->Used; j++) {
                            if (strncmp(V->Value[j], "LoadPriority:", 13) == 0) {
                                agent->priority = atoi(V->Value[j] + 13);

                                if (agent->priority > DMC_AGENT_DEFAULT_PRIORITY) {
                                    agent->priority = DMC_AGENT_DEFAULT_PRIORITY;
                                } else if (agent->priority < DMC_AGENT_MINIMUM_PRIORITY) {
                                    agent->priority = DMC_AGENT_MINIMUM_PRIORITY;
                                }

                                break;
                            }
                        }

                        MDBFreeValues(V);
                    }

                    if ((MDBRead(List->Value[i], MSGSRV_A_DISABLED, V) == 0) || (V->Value[0][0] != '1')) {
                        agent->flags |= DMC_FLAG_MODULE_ENABLED;
                    } else {
                        agent->flags &= ~DMC_FLAG_MODULE_ENABLED;
                    }

                    MDBFreeValues(V);
                } else {
                    agent->flags &= ~DMC_FLAG_MODULE_ENABLED;
                }

            } else {
                result = FALSE;
                break;
            }
        }

        continue;
    }
    return(result);
}

static BOOL
DMCLoadAgentsList()
{
    MDBValueStruct *list = NULL;
    BOOL bResult = FALSE;
    DMCAgent *head = NULL;
    DMCAgent *tail = NULL;
    MDBValueStruct *config = NULL;

    list = MDBCreateValueStruct(DMC.handles.directory, NULL);
    if (list) {
        config = MDBCreateValueStruct(DMC.handles.directory, NULL);
        if (config) {
            bResult = MDBEnumerateObjects(
                    DMC.dn,
                    NULL,
                    NULL,
                    list);
            if (bResult && list->Used) {
                bResult = DMCProcessAgentList(list, &head, &tail, config);
            }
            MDBDestroyValueStruct(config);
        }
        MDBFreeValues(list);
        MDBDestroyValueStruct(list);
    }
    if (bResult && DMC.webAdmin[0]) {
        DMCAgent *pAgent = MemMalloc(sizeof(DMCAgent));
        if (pAgent) {
            pAgent->flags = 0;
            pAgent->priority = DMC_AGENT_DEFAULT_PRIORITY;
            pAgent->data = NULL;
            pAgent->load = 0;
            pAgent->previous = NULL;
            if ((pAgent->next = head) != NULL) {
                head->previous = pAgent;
            } else {
                tail = pAgent;
            }
            head = pAgent;
            strcpy(pAgent->name, MSGSRV_NLM_WEBADMIN);
            if (DMCAgentPrep(pAgent) != FALSE) {
                config = MDBCreateValueStruct(DMC.handles.directory, NULL);
                if (config) {
                    if ((MDBRead(
                            DMC.webAdmin,
                            MSGSRV_A_DISABLED,
                            config) == 0) || (config->Value[0][0] != '1')) {
                        pAgent->flags |= DMC_FLAG_MODULE_ENABLED;
                    } else {
                        pAgent->flags &= ~DMC_FLAG_MODULE_ENABLED;
                    }
                    MDBFreeValues(config);
                    MDBDestroyValueStruct(config);
                }
            }
        } else {
            bResult = FALSE;
        }
    }
    if (bResult) {
        DMCSortAgentsByPriority(&head, &tail);
        DMC.agents.head = head;
        DMC.agents.tail = tail;
    }

    return(bResult);
}

static BOOL 
ReadConfiguration(void)
{
    int i;
    long tempoptions = -1;
    unsigned long used;
    unsigned long count;
    unsigned long port[2];
    unsigned char *ptr;
    unsigned char realDN[MDB_MAX_OBJECT_CHARS + 1];
    unsigned char realType[MDB_MAX_ATTRIBUTE_CHARS + 1];
    MDBValueStruct *config = NULL;
    MDBValueStruct *configDN = NULL;
    MDBValueStruct *address = NULL;

    if (!MsgGetConfigProperty(DMC.dn, MSGSRV_CONFIG_PROP_MESSAGING_SERVER)) {
        XplConsolePrintf("DMC: Messaging server not configured, shutting down.\r\n");

        /*MDBDestroyValueStruct(config);*/

        return(FALSE);
    }

    /*MDBFreeValues(config);*/
    config = MDBCreateValueStruct(DMC.handles.directory, NULL);
    if (MDBRead(DMC.dn, MSGSRV_A_CONFIG_CHANGED, config)) {
        DMC.monitor.version = atol(config->Value[0]);
    } else {
        DMC.monitor.version = 0;
    }

    MDBFreeValues(config);

    /*    Configuration attributes    */
    if (MDBRead(DMC.dn, MSGSRV_A_MONITOR_INTERVAL, config)) {
	DMC.monitor.interval = atol(config->Value[0]);
	if ((DMC.monitor.interval < 0) || (DMC.monitor.interval > 12 * 60 * 60)) {
	    DMC.monitor.interval = 12 * 60 * 60;
	}
    }
    MDBFreeValues(config);

    /* OBSOLETE
    if (MDBRead(DMC.dn, MSGSRV_A_CONFIGURATION, config)) {
        for (used = 0; used < config->Used; used++) {
            if (XplStrNCaseCmp(config->Value[used], "DMC: MonitorInterval=", 21) == 0) {
                DMC.monitor.interval = atol(config->Value[used] + 21);
                if ((DMC.monitor.interval < 0) || (DMC.monitor.interval > 12 * 60 * 60)) {
                    DMC.monitor.interval = 12 * 60 * 60;
                }
            }
        }
    }
    MDBFreeValues(config);
    */

    ptr = strrchr(DMC.dn, '\\');
    strcpy(DMC.rdn, ++ptr);

    address = MDBShareContext(config);
    configDN = MDBShareContext(config);

    /*    The local host must always be the first server in the list!    */
    if(MDBIsObject(DMC.dn, configDN)) {
        MDBFreeValues(configDN);

        if (MDBRead(DMC.dn, MSGSRV_A_IP_ADDRESS, address)) {
            port[0] = DMC_MANAGER_PORT;
            port[1] = DMC_MANAGER_SSL_PORT;

	    if (MDBRead(DMC.dn, MSGSRV_A_MANAGER_PORT, configDN)) {
		if (isdigit(configDN->Value[0])) {
		    port[0] = atol(configDN->Value[0]);
		} else {
		    port[0] = -1;
		}
	    }
	    MDBFreeValues(configDN);
	    if (MDBRead(DMC.dn, MSGSRV_A_MANAGER_SSL_PORT, configDN)) {
		if (isdigit(configDN->Value[0])) {
		    port[1] = atol(configDN->Value[0]);
		} else {
		    port[1] = -1;
		}
	    }
	    MDBFreeValues(configDN);
	    /* OBSOLETE
            MDBRead(DMC.dn, MSGSRV_A_CONFIGURATION, configDN);
            for (used = 0; used < configDN->Used; used++) {
                if (XplStrNCaseCmp(configDN->Value[used], "DMC: ManagerPort=", 17) == 0) {
                    if (isdigit(configDN->Value[used][17])) {
                        port[0] = atol(configDN->Value[used] + 17);
                    } else {
                        port[0] = -1;
                    }
                } else if (XplStrNCaseCmp(configDN->Value[used], "DMC: ManagerSSLPort=", 20) == 0) {
                    if (isdigit(configDN->Value[used][20])) {
                        port[1] = atol(configDN->Value[used] + 20);
                    } else {
                        port[1] = -1;
                    }
                }
            }
	    */

            DMC.server.port = (unsigned short) port[0];
            DMC.server.ssl.port = (unsigned short) port[1];

            if (AddManagedServer(DMC.dn, address->Value[0], (short) port[0], (short) port[1]) == FALSE) {
                XplConsolePrintf("DMC: Unable to manage the messaging server; shutting down\r\n");

                MDBDestroyValueStruct(configDN);
                MDBDestroyValueStruct(address);
                MDBDestroyValueStruct(config);

                return(FALSE);
            }
        } else {
            XplConsolePrintf("DMC: Messaging server address not available; shutting down\r\n");

            MDBDestroyValueStruct(configDN);
            MDBDestroyValueStruct(address);
            MDBDestroyValueStruct(config);

            return(FALSE);
        }
    } else {
        XplConsolePrintf("DMC: Messaging server not configured properly; shutting down\r\n");

        MDBDestroyValueStruct(configDN);
        MDBDestroyValueStruct(address);
        MDBDestroyValueStruct(config);

        return(FALSE);
    }

    MDBFreeValues(configDN);
    MDBFreeValues(address);

    DMC.server.ssl.options = DMC.client.ssl.options = SSL_OP_DONT_INSERT_EMPTY_FRAGMENTS;

    if (MDBRead(MSGSRV_ROOT, MSGSRV_A_SSL_TLS, config)) {
	if (atoi(config->Value[0])) {
	    tempoptions |= SSL_ALLOW_SSL3;
	} else {
	    tempoptions = 0;
	}
    }
    MDBFreeValues(config);
    if (MDBRead(MSGSRV_ROOT, MSGSRV_A_SSL_ALLOW_CHAINED, config)) {
	if (atoi(config->Value[0])) {
	    tempoptions |= SSL_ALLOW_CHAIN;
	} else if (tempoptions == -1){
	    tempoptions = 0;
	}
    }
    MDBFreeValues(config);
    if(tempoptions != -1) {
	DMC.server.ssl.options = tempoptions;
	DMC.client.ssl.options = DMC.server.ssl.options;
	DMC.server.ssl.options &= ~SSL_USE_CLIENT_CERT;
    }
    /* OBSOLETE
    if (MDBRead(MSGSRV_ROOT, MSGSRV_A_SSL_OPTIONS, config)) {
        DMC.server.ssl.options = atoi(config->Value[0]);                                                                                    
        DMC.client.ssl.options = DMC.server.ssl.options;
        DMC.server.ssl.options &= ~SSL_USE_CLIENT_CERT;
    }    
    MDBFreeValues(config);
    */

    if (MDBRead(MSGSRV_ROOT, MSGSRV_A_ACL, config)) {
        HashCredential(DMC.dn, config->Value[0], DMC.credential);
    }    
        
    MDBFreeValues(config);

    /* First check all "real" classes    */
    if (MDBEnumerateObjects(MSGSRV_ROOT, MSGSRV_C_SERVER, NULL, config)) {
        for (used = 0; used < config->Used; used++) {
            MDBGetObjectDetails(config->Value[used], NULL, NULL, realDN, configDN);

            /*    Skip the local host, it has already been added.    */
            if (XplStrCaseCmp(DMC.dn, realDN) != 0) {
                if (MDBRead(realDN, MSGSRV_A_IP_ADDRESS, address)) {
                    MDBFreeValues(configDN);

                    port[0] = DMC_MANAGER_PORT;
                    port[1] = DMC_MANAGER_SSL_PORT;

                    MDBRead(realDN, MSGSRV_A_CONFIGURATION, configDN);
                    for (count = 0; count < configDN->Used; count++) {
                        if (XplStrNCaseCmp(configDN->Value[count], "DMC: ManagerPort=", 17) == 0) {
                            if (isdigit(configDN->Value[count][17])) {
                                port[0] = atol(configDN->Value[count] + 17);
                            } else {
                                port[0] = -1;
                            }
                        } else if (XplStrNCaseCmp(configDN->Value[count], "DMC: ManagerSSLPort=", 20) == 0) {
                            if (isdigit(configDN->Value[count][20])) {
                                port[1] = atol(configDN->Value[count] + 20);
                            } else {
                                port[1] = -1;
                            }
                        }
                    }

                    AddManagedServer(realDN, address->Value[0], (short) port[0], (short) port[1]);

                    MDBFreeValues(address);
                }
            }

            MDBFreeValues(configDN);
        }
    }

    MDBFreeValues(config);

    /* Now check any aliases that might be in the Internet Services container */
    if (MDBEnumerateObjects(MSGSRV_ROOT, C_ALIAS, NULL, config)) {
        for (used = 0; used < config->Used; used++) {
            MDBGetObjectDetails(config->Value[used], realType, NULL, realDN, configDN);

            if ((XplStrCaseCmp(realType, MSGSRV_C_SERVER) == 0) 
                    && (XplStrCaseCmp(DMC.dn, realDN) != 0)) {
                if (MDBRead(realDN, MSGSRV_A_IP_ADDRESS, address)) {
                    MDBFreeValues(configDN);

                    port[0] = DMC_MANAGER_PORT;
                    port[1] = DMC_MANAGER_SSL_PORT;

                    MDBRead(realDN, MSGSRV_A_CONFIGURATION, configDN);
                    for (count = 0; count < configDN->Used; count++) {
                        if (XplStrNCaseCmp(configDN->Value[count], "DMC: ManagerPort=", 17) == 0) {
                            if (isdigit(configDN->Value[count][17])) {
                                port[0] = atol(configDN->Value[count] + 17);
                            } else {
                                port[0] = -1;
                            }
                        } else if (XplStrNCaseCmp(configDN->Value[count], "DMC: ManagerSSLPort=", 20) == 0) {
                            if (isdigit(configDN->Value[count][20])) {
                                port[1] = atol(configDN->Value[count] + 20);
                            } else {
                                port[1] = -1;
                            }
                        }
                    }

                    AddManagedServer(realDN, address->Value[0], (short) port[0], (short) port[1]);
                }
            }

            MDBFreeValues(configDN);
            MDBFreeValues(address);
        }
    }

    MDBDestroyValueStruct(configDN);
    configDN = NULL;

    MDBDestroyValueStruct(address);
    address = NULL;

    /*    Trusted hosts    */
    MDBSetValueStructContext(DMC.dn, config);

    if (MDBRead(MSGSRV_AGENT_STORE, MSGSRV_A_CONFIG_CHANGED, config)) {
        DMC.trusted.version = atol(config->Value[0]);
    } else {
        DMC.trusted.version = 0;
    }

    MDBFreeValues(config);
    DMC.trusted.count = MDBRead(MSGSRV_AGENT_STORE, MSGSRV_A_STORE_TRUSTED_HOSTS, config);

#if defined(NETWARE) || defined(LIBC)
    DMC.trusted.count += 2;
#else
    DMC.trusted.count++;
#endif

    if (DMC.trusted.count) {
        DMC.trusted.hosts = (unsigned long *)MemMalloc(DMC.trusted.count * sizeof(unsigned long));

        if (DMC.trusted.hosts != NULL) {
            for (used = 0; used < config->Used; used++) {
                DMC.trusted.hosts[used] = inet_addr(config->Value[used]);
            }

#if defined(NETWARE) || defined(LIBC)
            DMC.trusted.hosts[used++] = inet_addr("127.0.0.1");
            DMC.trusted.hosts[used++] = MsgGetHostIPAddress();
#else
            DMC.trusted.hosts[used++] = MsgGetHostIPAddress();
#endif
        } else {
            XplConsolePrintf("DMC: Unable to allocate memory for trusted hosts.\r\n");
            DMC.trusted.count = 0;
        }
    }

    MDBFreeValues(config);

    /*
    i = MDBReadDN(host, WA_A_CONFIG_DN, config);
    if (i) {
        strcpy(DMC.webAdmin, config->Value[0]);
    } else {
    */
    if (!MsgGetConfigProperty(DMC.webAdmin, 
                              MSGSRV_CONFIG_PROP_WEB_ADMIN_SERVER)) {
#if VERBOSE
        XplConsolePrintf(
                "Bongo web administration server not configured; skipping.\r\n");
#endif
        DMC.webAdmin[0] = '\0';
    }
    MDBDestroyValueStruct(config);

    DMCLoadAgentsList();

    return(TRUE);
}

#if defined(NETWARE) || defined(LIBC) || defined(WIN32)
int _NonAppCheckUnload(void)
{
    static BOOL    checked = FALSE;
    XplThreadID    id;

    if (!checked) {
        checked = DMC.state = DMC_UNLOADING;

        /*    Delay shutdown of the management console!    */
        XplDelay(3000);

        XplWaitOnLocalSemaphore(DMC.sem.shutdown);

        id = XplSetThreadGroupID(DMC.id.group);
        if (DMC.server.conn != NULL) {
            ConnFree(DMC.server.conn);
            DMC.server.conn = NULL;
        }
        XplSetThreadGroupID(id);

        XplWaitOnLocalSemaphore(DMC.sem.main);
    }

    return(0);
}
#endif

XplServiceCode(DMCSignalHandler)

int XplServiceMain(int argc, char *argv[])
{
    int ccode;
    XplThreadID id;

    if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
	XplConsolePrintf("bongodmc: Could not drop to unprivileged user '%s', exiting.\n", MsgGetUnprivilegedUser());
	return 1;
    }

    DMC.state = DMC_INITIALIZING;
    DMC.stopped = FALSE;

    XplSignalHandler(DMCSignalHandler);

    DMC.startUpTime = time(NULL);

    DMC.id.main = XplGetThreadID();
    DMC.id.group = XplGetThreadGroupID();

    DMC.dn[0] = '\0';
    DMC.rdn[0] = '\0';
    DMC.credential[0] = '\0';

    LoadProtocolCommandTree(&DMC.commands, DMCCommandEntries);
    LoadProtocolCommandTree(&DMC.bridged, DMCBridgedCommandEntries);

    XplSafeWrite(DMC.server.active, 0);

    DMC.server.conn = NULL;
    DMC.server.managed.count = 0;
    DMC.server.managed.list = NULL;
    DMC.server.ssl.enable = FALSE;
    DMC.server.ssl.options = 0;
    DMC.server.ssl.context = NULL;

    DMC.client.sleepTime = 3 * 60;
    DMC.client.ssl.enable = FALSE;
    DMC.client.ssl.options = 0;
    DMC.client.ssl.context = NULL;

    XplSafeWrite(DMC.client.worker.maximum, 10000);
    XplSafeWrite(DMC.client.worker.active, 0);
    XplSafeWrite(DMC.client.worker.idle, 0);

    DMC.monitor.enable = FALSE;
    DMC.monitor.interval = (5 * 60);

    DMC.trusted.count = 0;
    DMC.trusted.hosts = NULL;

    DMC.stats.base = &DMC.stats.data.base;
    DMC.stats.server = &DMC.stats.data.server;
    DMC.stats.spam = &DMC.stats.data.spam;

    DMC.handles.logging = NULL;
    DMC.handles.directory = NULL;

    if (MemoryManagerOpen(MSGSRV_AGENT_DMC) != -1) {
        XplOpenLocalSemaphore(DMC.sem.main, 0);
        XplOpenLocalSemaphore(DMC.sem.shutdown, 1);
        XplOpenLocalSemaphore(DMC.client.semaphore, 1);
        XplOpenLocalSemaphore(DMC.client.worker.todo, 1);
    } else {
        XplBell();
        XplConsolePrintf("DMC: Memory manager failed to initialize; exiting!\r\n");
        XplBell();

        exit(-1);
    }

    DMC.client.pool = MemPrivatePoolAlloc("DMC Connections", sizeof(DMCClient), 0, 16384, TRUE, FALSE, ClientAllocCB, NULL, NULL);
    if (DMC.client.pool != NULL) {
        SetCurrentNameSpace(NWOS2_NAME_SPACE);
    } else {
        XplBell();
        XplConsolePrintf("DMC: Out of memory - cannot allocate connection pool; exiting!\n");
        XplBell();

        MemoryManagerClose(MSGSRV_AGENT_DMC);

        exit(-1);
    }

    ConnStartup(DMC_CONNECTION_TIMEOUT, TRUE);

    MDBInit();

    DMC.handles.directory = (MDBHandle)MsgInit();
    if (DMC.handles.directory == NULL) {
        XplBell();
        XplConsolePrintf("DMC: Invalid directory credentials; exiting!\n");
        XplBell();

        CONN_TRACE_SHUTDOWN();
        ConnShutdown();

        MemoryManagerClose(MSGSRV_AGENT_DMC);

        return(-1);
    }

    XplRWLockInit(&(DMC.lock));

    DMC.handles.logging = LoggerOpen("bongodmc");
    
    if (DMC.handles.logging == NULL) {
        XplConsolePrintf("DMC: Unable to initialize logging interface.  Logging disabled.\r\n");
    }

    CONN_TRACE_INIT((char *)MsgGetWorkDir(NULL), "dmc");
    // CONN_TRACE_SET_FLAGS(CONN_TRACE_ALL); /* uncomment this line and pass '--enable-conntrace' to autogen to get the agent to trace all connections */

    if (ReadConfiguration() != -1) {
        if (ServerSocketInit () < 0) {
            XplConsolePrintf("bongodmc: Exiting.\n");
            return 1;
        }

        if (ServerSocketSSLInit() >= 0) {
            /* Done binding to ports, drop privs permanently */
            if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
                XplConsolePrintf("bongodmc: Could not drop to unprivileged user '%s', exiting.\n", MsgGetUnprivilegedUser());
                return 1;
            }

            DMC.server.ssl.config.method = SSLv23_server_method;
            DMC.server.ssl.config.options = SSL_OP_DONT_INSERT_EMPTY_FRAGMENTS;
            DMC.server.ssl.config.mode = SSL_MODE_AUTO_RETRY;
            DMC.server.ssl.config.cipherList = NULL;
            DMC.server.ssl.config.certificate.type = SSL_FILETYPE_PEM;
            DMC.server.ssl.config.certificate.file = MsgGetTLSCertPath(NULL);
            DMC.server.ssl.config.key.type = SSL_FILETYPE_PEM;
            DMC.server.ssl.config.key.file = MsgGetTLSKeyPath(NULL);

            DMC.client.ssl.config.method = SSLv23_client_method;
            DMC.client.ssl.config.options = SSL_OP_DONT_INSERT_EMPTY_FRAGMENTS;
            DMC.client.ssl.config.mode = SSL_MODE_AUTO_RETRY;
            DMC.client.ssl.config.cipherList = NULL;
            DMC.client.ssl.config.certificate.type = SSL_FILETYPE_PEM;
            DMC.client.ssl.config.certificate.file = MsgGetTLSCertPath(NULL);
            DMC.client.ssl.config.key.type = SSL_FILETYPE_PEM;
            DMC.client.ssl.config.key.file = MsgGetTLSKeyPath(NULL);

            DMC.server.ssl.enable = FALSE;
            DMC.client.ssl.enable = FALSE;

            DMC.client.ssl.context = ConnSSLContextAlloc(&(DMC.client.ssl.config));
            if (DMC.client.ssl.context) {
                DMC.client.ssl.enable = TRUE;

                DMC.server.ssl.context = ConnSSLContextAlloc(&(DMC.server.ssl.config));
                if (DMC.server.ssl.context) {
                    XplBeginCountedThread(&id, DMCSSLServer, DMC_CONNECTION_STACK_SIZE, NULL, ccode, DMC.server.active);

                    DMC.server.ssl.enable = TRUE;
                }
            }
        }
	
	/* Done binding to ports, drop privs permanently */
	if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
	    XplConsolePrintf("bongodmc: Could not drop to unprivileged user '%s', exiting.\n", MsgGetUnprivilegedUser());
	    return 1;
	}

        DMC.state = DMC_LOADING;

        /* Start the DMC Server */
        XplStartMainThread(PRODUCT_SHORT_NAME, &id, DMCServer, DMC_MANAGEMENT_STACKSIZE, NULL, ccode);
    }

    XplUnloadApp(XplGetThreadID());
    return(0);
}

int 
DMCCommandAuth(void *param)
{
    int i;
    unsigned char *ptr;
    unsigned char digest[16];
    unsigned char credential[33];
    MD5_CTX mdContext;
    DMCClient *client = param;
    int ccode = -1;

    if (client->state == CLIENT_STATE_FRESH) {
        if (client->buffer[4] == ' ') {
            ptr = strchr(client->buffer + 5, ' ');
            if (!ptr) {
                ptr = &client->buffer[5];
                MD5_Init(&mdContext);
                MD5_Update(&mdContext, DMC.rdn, strlen(DMC.rdn));
                MD5_Update(&mdContext, DMC.credential, DMC_HASH_SIZE);
                MD5_Final(digest, &mdContext);
                for (i = 0; i < 16; i++) {
                    sprintf(credential + (i * 2), "%02x", digest[i]);
                }
                if (QuickCmp(credential, ptr)) {
                    for (i = 0; i < (int) DMC.server.managed.count; i++) {
                        if (XplStrCaseCmp(
                                    DMC.server.managed.list[i].identity +
                                        DMC.server.managed.list[i].rdnOffset,
                                    DMC.rdn) == 0) {
                            client->server = &(DMC.server.managed.list[i]);
                            client->state = CLIENT_STATE_SERVER;
                            ccode = ConnWrite(
                                    client->user,
                                    DMCMSG1000OK,
                                    sizeof(DMCMSG1000OK) - 1);
                            break;
                        }
                    }
                    if (ccode == -1)
                    {
                        ConnWrite(
                                client->user,
                                DMCMSG3242BADAUTH,
                                sizeof(DMCMSG3242BADAUTH) - 1);
                        XplDelay(2000);
                    }
                } else {
                    ConnWrite(
                            client->user,
                            DMCMSG3242BADAUTH,
                            sizeof(DMCMSG3242BADAUTH) - 1);
                    XplDelay(2000);
                }
            } else {
                ccode = ConnWrite(
                        client->user,
                        DMCMSG2101BADPARAMS,
                        sizeof(DMCMSG2101BADPARAMS) - 1);
            }
        } else {
            ccode = ConnWrite(
                    client->user,
                    DMCMSG2100BADCOMMAND,
                    sizeof(DMCMSG2100BADCOMMAND) - 1);
        }
    } else {
        ccode = ConnWrite(
                client->user,
                DMCMSG2102BADSTATE,
                sizeof(DMCMSG2102BADSTATE) - 1);
    }
    return(ccode);
}

static int 
DMCCommandStart(void *param)
{
    DMCClient *pClient = param;
    int ccode = -1;

    switch (pClient->state) {
        case CLIENT_STATE_SERVER: {
            if (pClient->state == CLIENT_STATE_SERVER)
            {
                DMCAgent *agent = DMC.agents.head;

                while (agent) {
                    if ((agent->flags & DMC_FLAG_MODULE_ENABLED) 
                            && !(agent->flags & DMC_FLAG_MODULE_LOADED)) {
                        if (DMCStartAgent(agent) == TRUE) {
                            agent->flags |= DMC_FLAG_MODULE_LOADED;
                        }
                    }
                    agent = agent->next;
                }
                ccode = ConnWrite(
                        pClient->user,
                        DMCMSG1000OK,
                        sizeof(DMCMSG1000OK) - 1);
            }
            break;
        }
        case CLIENT_STATE_CLOSING:
        case CLIENT_STATE_FRESH:
        case CLIENT_STATE_AGENT:
        case CLIENT_STATE_AUTHENTICATED:
        default: {
            ccode = ConnWrite(
                    pClient->user,
                    DMCMSG2102BADSTATE,
                    sizeof(DMCMSG2102BADSTATE) - 1);
            break;
        }
    }
    return(ccode);
}

static int
DMCCommandShutdown(void *param)
{
    DMCClient *client = param;
    int ccode = -1;
    ManagedServer *server = NULL;
    ManagedAgent *agent = NULL;
    Connection *pConn;
    int indx;

    XplConsolePrintf("bongodmc: Shutting down...\r\n");

    server = client->server;
    if (server) {
        for (indx = 0; indx < (int) server->agents.count; indx++) {
            agent = &server->agents.list[indx];
            if (agent->flags & AGENT_FLAG_REGISTERED) {
                pConn = ConnAlloc(TRUE);
                if (pConn) {
                    memset(
                        &(pConn->socketAddress),
                            0,
                            sizeof(pConn->socketAddress));
                    pConn->socketAddress.sin_family = AF_INET;
                    pConn->socketAddress.sin_addr.s_addr =
                            agent->address.clear.sin_addr.s_addr;
                    pConn->socketAddress.sin_port =
                        agent->address.clear.sin_port;
                    if (pConn->socketAddress.sin_port != (unsigned short) -1) {
                        ccode = ConnConnect(pConn, NULL, 0, NULL);
                    } else if (DMC.client.ssl.enable &&
                            ((pConn->socketAddress.sin_port =
                                agent->address.ssl.sin_port) !=
                                    (unsigned short)-1)) {
                        ccode = ConnConnect(
                                pConn,
                                NULL,
                                0,
                                DMC.client.ssl.context);
                    } else {
                        ccode = -1;
                        break;
                    }
                    if (ccode != -1)
                    {
                            ConnWrite(pConn, "CMD 1", 5);
                            ConnWrite(pConn, "\r\n", 2);
                            ConnFlush(pConn);
                            client->buffer[0] = '\0';
                            ccode = DMCReadAnswer(
                                    pConn,
                                    client->buffer,
                                    CONN_BUFSIZE,
                                    TRUE);
                        if (ccode == 1000) {
                            ;
                        }
                        ConnClose(pConn, 0);
                    }
                }
            }
        }
    }

    for (indx = 0; (indx < 20) && (server->agents.registered > 0); indx++) {
        XplDelay(1000);
    }
    ccode = ConnWrite(
            client->user,
            DMCMSG2004SERVER_DOWN,
            sizeof(DMCMSG2004SERVER_DOWN) - 1);
    ConnFlush(client->user);
    DMC.state = DMC_STOPPING;
    IPshutdown(DMC.server.conn->socket, 2);
    DMCKillAgents();
    return(ccode);;
}

void 
DMCUnload(void)
{
    DMCUnloadConfiguration();
    if (DMC.handles.directory) {
        MsgShutdown();
    }
    ConnShutdown();
    if (DMC.handles.logging) {
        LoggerClose(DMC.handles.logging);
        DMC.handles.logging = NULL;
    }
    /*
    **  Cleanup SSL
    */
    if (DMC.client.ssl.context) {
        ConnSSLContextFree(DMC.client.ssl.context);
        DMC.client.ssl.context= NULL;
    }
    if (DMC.server.ssl.context) {
        ConnSSLContextFree(DMC.server.ssl.context);
        DMC.server.ssl.context = NULL;
    }
    XplRWLockDestroy(&DMC.lock);
    XPLCryptoLockDestroy();
    IPCleanup();
    return;
}


static void 
DMCUnloadConfiguration(void)
{
    DMCAgent *agent;

    agent = DMC.agents.head;
    DMC.agents.head = NULL;
    DMC.agents.tail = NULL;
    DMC.agents.count = 0;
    while (agent) {
        DMCAgentRelease(agent);
        agent = agent->next;
    }
    return;
}

static void 
DMCSortAgentsByPriority(DMCAgent **Head, DMCAgent **Tail)
{
    DMCAgent	*head = *Head;
    DMCAgent	*tail = *Tail;
    DMCAgent	*agent = *Head;

    while (agent->next != NULL) {
        if (agent->next->priority < agent->priority) {
            if ((agent->next->previous = agent->previous) != NULL) {
                agent->previous->next = agent->next;
            } else {
                head = agent->next;
            }
            agent->previous = agent->next;
            if ((agent->next = agent->previous->next) != NULL) {
                agent->next->previous = agent;
            } else {
                tail = agent;
            }
            agent->previous->next = agent;
            agent = head;
            continue;
        }
        agent = agent->next;
    }
    *Head = head;
    *Tail = tail;
    return;
}

static int
DMCReadAnswer(
        Connection *pConn,
        unsigned char *response,
        size_t length,
        BOOL checkForResult)
{
    int len;
    int result = -1;
    size_t count;
    unsigned char *cur;

    len = ConnReadAnswer(pConn, response, length);
    if (len > 0) {
        cur = response + 4;
        if (checkForResult &&
                ((*cur == ' ') ||
                 (*cur == '-') ||
                 ((cur = strchr(response, ' ')) != NULL))) {
            *cur++ = '\0';
            result = atoi(response);
            count = len - (cur - response);
            memmove(response, cur, count);
            response[count] = '\0';
        } else {
            result = atoi(response);
        }
    }
    return(result);
}

