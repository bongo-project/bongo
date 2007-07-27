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
 * GNU General Public License for more etails.
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
#include <bongoutil.h>
#include <logger.h>
#include <nmlib.h>
#include <xplresolve.h>
#include <bongoagent.h>

#include "mailprox.h"

MailProxyGlobals MailProxy;

#if defined(RELEASE_BUILD)
#define GetMailProxyClientPoolEntry()            MemPrivatePoolGetEntry(MailProxy.client.pool)
#else
#define GetMailProxyClientPoolEntry()            MemPrivatePoolGetEntryDebug(MailProxy.client.pool, __FILE__, __LINE__)
#endif

static void SignalHandler(int sigtype);

#define QUEUE_WORK_TO_DO(q, id, r) \
        { \
            XplWaitOnLocalSemaphore(MailProxy.client.semaphore); \
            if (XplSafeRead(MailProxy.client.worker.idle)) { \
                (q)->previous = NULL; \
                if (((q)->next = MailProxy.client.worker.head) != NULL) { \
                    (q)->next->previous = (q); \
                } else { \
                    MailProxy.client.worker.tail = (q); \
                } \
                MailProxy.client.worker.head = (q); \
                (r) = 0; \
            } else if (XplSafeRead(MailProxy.client.worker.active) < XplSafeRead(MailProxy.client.worker.maximum)) { \
                XplSafeIncrement(MailProxy.client.worker.active); \
                XplSignalBlock(); \
                XplBeginThread(&(id), HandleConnection, PROXY_STACK_SPACE, XPL_INT_TO_PTR(XplSafeRead(MailProxy.client.worker.active)), (r)); \
                XplSignalHandler(SignalHandler); \
                if (!(r)) { \
                    (q)->previous = NULL; \
                    if (((q)->next = MailProxy.client.worker.head) != NULL) { \
                        (q)->next->previous = (q); \
                    } else { \
                        MailProxy.client.worker.tail = (q); \
                    } \
                    MailProxy.client.worker.head = (q); \
                } else { \
                    XplSafeDecrement(MailProxy.client.worker.active); \
                    (r) = -1; \
                } \
            } else { \
                (r) = -1; \
            } \
            XplSignalLocalSemaphore(MailProxy.client.semaphore); \
        }

#if defined(NETWARE) || defined(LIBC) || defined(WIN32)
int 
_NonAppCheckUnload(void)
{
    int s;
    static BOOL checked = FALSE;
    XplThreadID id;

    if (!checked) {
        checked = MailProxy.state = MAILPROXY_STATE_UNLOADING;

        XplWaitOnLocalSemaphore(MailProxy.sem.shutdown);

        id = XplSetThreadGroupID(MailProxy.id.group);
        if (MailProxy.server.conn->socket != -1) {
            s = MailProxy.server.conn->socket;
            MailProxy.server.conn->socket = -1;

            IPclose(s);
        }
        XplSetThreadGroupID(id);

        XplWaitOnLocalSemaphore(MailProxy.sem.main);
    }

    return(0);
}
#endif

static void 
SignalHandler(int sigtype)
{
    switch(sigtype) {
        case SIGHUP: {
            if (MailProxy.state < MAILPROXY_STATE_UNLOADING) {
                MailProxy.state = MAILPROXY_STATE_UNLOADING;
            }

            break;
        }
        case SIGINT:
        case SIGTERM: {
            if (MailProxy.state == MAILPROXY_STATE_STOPPING) {
                XplUnloadApp(getpid());
            } else if (MailProxy.state < MAILPROXY_STATE_STOPPING) {
                MailProxy.state = MAILPROXY_STATE_STOPPING;
            }

            break;
        }

        case SIGUSR1: {
            MailProxy.force = TRUE;
            break;
        }

        default: {
            break;
        }
    }

    return;
}

static BOOL 
MailProxyClientAllocCB(void *buffer, void *data)
{
    register MailProxyClient *c = (MailProxyClient *)buffer;

    memset(c, 0, sizeof(MailProxyClient));
    c->state = MAILPROXY_CLIENT_FRESH;

    return(TRUE);
}

static void 
MailProxyClientFree(MailProxyClient *client)
{
    register MailProxyClient *c = client;

    memset(c, 0, sizeof(MailProxyClient));
    c->state = MAILPROXY_CLIENT_FRESH;

    MemPrivatePoolReturnEntry(c);

    return;
}

static BOOL
FreeClientData(MailProxyClient *client)
{
    ProxyAccount *account;
    ProxyAccount *next;
    ProxyUID *uid;
    ProxyUID *nextuid;

    if (client) {
        if (client->state != MAILPROXY_CLIENT_ENDING) {
            client->state = MAILPROXY_CLIENT_ENDING;

            if (client->conn) {
                ConnFree(client->conn);
                client->conn = NULL;
            }

            if (client->nmap) {
                NMAPQuit(client->nmap);
                ConnFree(client->nmap);
                client->nmap = NULL;
            }

            account = client->list;
            while (account) {
                uid = account->uidList.next;
                if (account->uidList.folderPath)
                    MemFree(account->uidList.folderPath);
                
                while (uid) {
                    nextuid = uid->next;
                    if (uid->folderPath)
                        MemFree(uid->folderPath);
                    MemFree(uid);
                    uid = nextuid;
                }
                next = account->next;
                MemFree(account);
                account = next;
            }

            if (client->q) {
                if (client->q->accounts) {
                    MDBDestroyValueStruct(client->q->accounts);
                    client->q->accounts = NULL;
                }

                MemFree(client->q);
                client->q = NULL;
            }
        }
    }

    return(TRUE);
}

static __inline void 
ParseAccounts(MailProxyClient *client)
{
    unsigned long used;
    unsigned long i;
    unsigned long len;
    unsigned char *ptr;
    unsigned char *ptr2;
    ProxyAccount list;
    ProxyAccount *next;
    ProxyUID *uid;

    /* Originally stored as: Title<CR>Host<CR>User<CR>Password<CR>UID<CR>IMAP<CR>KeepMail */
    /* IMAP stored as: Title<CR>Host<CR>User<CR>Password<CR>0x7FUID count<CR>UID<CR>FolderPath<CR>...<CR>IMAP<CR>KeepMail */
    for (used = 0; used < client->q->accounts->Used; used++) {
        memset(&list, 0, sizeof(ProxyAccount));

        ptr = client->q->accounts->Value[used];
        if ((ptr2 = strchr(ptr, 0x0D)) != NULL) {
            *ptr2++ = '\0';

            list.title = ptr;
            ptr = ptr2;
        } else {
            continue;
        }

        if ((ptr2 = strchr(ptr2, 0x0D)) != NULL) {
            *ptr2++ = '\0';

            list.host = ptr;
            ptr = ptr2;
        } else {
            continue;
        }

        if ((ptr2 = strchr(ptr2, 0x0D)) != NULL) {
            *ptr2++ = '\0';

            list.user = ptr;
            ptr = ptr2;
        } else {
            continue;
        }

        if ((ptr2 = strchr(ptr2, 0x0D)) != NULL) {
            *ptr2++ = '\0';

            list.password = ptr;
            ptr = ptr2;
        } else {
            continue;
        }

        if ((ptr2 = strchr(ptr2, 0x0D)) != NULL) {
            *ptr2++ = '\0';

            if (ptr[0] == 0x7F) {
                uid = NULL;
                
                list.uidCount = atol(ptr + 1);
                ptr = ptr2;
                for (i = 0; i < list.uidCount; i++) {
                    if (uid != NULL) {
                        uid->next = (ProxyUID *)MemMalloc(sizeof(ProxyUID));
                        uid = uid->next;
                    } else {
                        uid = &list.uidList;
                    }
            
                    if (uid != NULL) {
                        uid->next = NULL;
                        uid->folderPath = NULL;
                        
                        if ((ptr2 = strchr(ptr2, 0x0D)) != NULL) {
                            *ptr2++ = '\0';
                
                            uid->uid = atol(ptr);
                            ptr = ptr2;
                            
                            if ((ptr2 = strchr(ptr2, 0x0D)) != NULL) {
                                *ptr2++ = '\0';

                                len = strlen(ptr) + 1;
                                uid->folderPath = (unsigned char *)MemMalloc(len);
                                if (uid->folderPath) {
                                    strcpy(uid->folderPath, ptr);
                                } else {
                                    LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, client->q->user, len, __LINE__, NULL, 0);
                                }
                                ptr = ptr2;
                            }
                        }
                    } else {
                        LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, client->q->user, sizeof(ProxyUID), __LINE__, NULL, 0);
                    }
                }
                
                if (*ptr2++ == '1')
                    list.flags |= MAILPROXY_FLAG_ALLFOLDERS;

                ptr2++; // skip past 0x0D
            }
            else {
                if (*ptr2 == '1') {
                    list.uidCount = 1;
                    list.uidList.uid = atoi(ptr);
                    list.uidList.folderPath = (unsigned char *)MemMalloc(6);
                    if (list.uidList.folderPath) {
                        strcpy(list.uidList.folderPath, "INBOX");
                    } else {
                        LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, client->q->user, 6, __LINE__, NULL, 0);
                    }
                } else {
                    list.uid = ptr;
                }
            }

            if (*ptr2++ == '1') {
                list.flags |= MAILPROXY_FLAG_IMAP;
            } else {
                list.flags |= MAILPROXY_FLAG_POP;
            }

            if (*ptr2++ == 0x0D) {
                if (*ptr2++ == '1') {
                    list.flags |= MAILPROXY_FLAG_LEAVE_MAIL;
                }

                if (*ptr2++ == 0x0D) {
                    if (*ptr2 == '1') {
                        ptr2++;
                        list.flags |= MAILPROXY_FLAG_SSL;
                    }
                }

                if (list.flags & MAILPROXY_FLAG_IMAP) {
                    if (list.flags & MAILPROXY_FLAG_SSL) {
                        list.port = PORT_IMAP_SSL;
                    } else {
                        list.port = PORT_IMAP;
                    }
                } else {
                    if (list.flags & MAILPROXY_FLAG_SSL) {
                        list.port = PORT_POP3_SSL;
                    } else {
                        list.port = PORT_POP3;
                    }
                }

                next = (ProxyAccount *)MemMalloc(sizeof(ProxyAccount));
                if (next) {
                    list.flags |= MAILPROXY_FLAG_ENABLED;
                    memcpy(next, &list, sizeof(ProxyAccount));
                    next->next = client->list;
                    client->list = next;
                } else {
                    LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, client->q->user, sizeof(ProxyAccount), __LINE__, NULL, 0);
                }
            }
        }
    }

    return;
}

static __inline void 
ProxyUser(MailProxyClient *client)
{
    int ccode;
    int count = 0;
    int allocSize = 1024;
    unsigned long i;
    unsigned char *ptr;
    unsigned char *port;
    unsigned char *pref;
    BOOL update;
    ProxyAccount *proxy;
    ProxyUID *nextuid;
    MDBValueStruct *accounts;

    accounts = MDBCreateValueStruct(MailProxy.handle.directory, client->q->context);
    if (accounts) {
        ParseAccounts(client);

        proxy = client->list;
        update = FALSE;

        XplSafeIncrement(MailProxy.stats.serviced);
    } else {
        return;
    }

    while (proxy != NULL) {
        if ((proxy->flags & MAILPROXY_FLAG_ENABLED) 
                && (strchr(proxy->host, '.') != NULL)) {
            port = strchr(proxy->host, ':');
            if (port) {
                *port = '\0';
                proxy->port = atoi(port + 1);
            }

            ptr = proxy->host;
            while (*ptr) {
                if (isupper(*ptr)) {
                    *ptr++;
                    continue;
                }

                *ptr = toupper(*ptr);
                ptr++;
            }

            /* fixme - enhancement - setup a disallowed domain list to prevent proxies to configured domains?
            LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_DISALLOWED_HOSTNAME, LOG_WARNING, 0, proxy->host, client->q->user, 0, 0, NULL, 0);
            */

            if (proxy->flags & MAILPROXY_FLAG_IMAP) {
                ccode = ProxyIMAPAccount(client, proxy);

                if (client->conn) {
                    ConnWrite(client->conn, "F99 logout\r\n", 12);
                    ConnFlush(client->conn);
                    ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE);
                    ConnFree(client->conn);
                    client->conn = NULL;
                }
            } else if (proxy->flags & MAILPROXY_FLAG_POP) {
                ccode = ProxyPOP3Account(client, proxy);

                if (client->conn) {
                    ConnWrite(client->conn, "QUIT\r\n", 6);
                    ConnFlush(client->conn);
                    ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE);
                    ConnFree(client->conn);
                    client->conn = NULL;
                }
            } else {
                ccode = -1;
            }

            if (client->nmap) {
                NMAPQuit(client->nmap);
                ConnFree(client->nmap);
                client->nmap = NULL;
            }

            if ((ccode != -1) && (proxy->flags & MAILPROXY_FLAG_STORE_UID)) {
                update  = TRUE;
            }

            if (proxy->flags & (MAILPROXY_FLAG_BAD_PASSWORD | MAILPROXY_FLAG_BAD_HOST | MAILPROXY_FLAG_BAD_HANDSHAKE | MAILPROXY_FLAG_BAD_PROXY)) {
                /* fixme - notify user of proxy attempt failure */
            }

            if (proxy->flags & MAILPROXY_FLAG_IMAP) {
                nextuid = &proxy->uidList;
                                
                for (i = 0; i < proxy->uidCount; i++) {
                    allocSize += strlen(nextuid->folderPath) + 16;
                    nextuid = nextuid->next;
                }
                
                pref = (unsigned char *)MemMalloc(allocSize);
            
                sprintf(pref, "Entry %d\r%s\r%s\r%s\r\x7f%ld\r", 
                    ++count, 
                    proxy->host, 
                    proxy->user, 
                    proxy->password, 
                    proxy->uidCount);
    
                nextuid = &proxy->uidList;
                                
                for (i = 0; i < proxy->uidCount; i++) {
                    sprintf(&pref[strlen(pref)], "%ld\r%s\r", nextuid->uid, nextuid->folderPath);
                    nextuid = nextuid->next;
                }
                
                sprintf(&pref[strlen(pref)], "%c\r", (proxy->flags & MAILPROXY_FLAG_ALLFOLDERS)? '1': '0');
            } else {
                pref = (unsigned char *)MemMalloc(allocSize);
            
                sprintf(pref, "Entry %d\r%s\r%s\r%s\r%s\r", 
                    ++count, 
                    proxy->host, 
                    proxy->user, 
                    proxy->password, 
                    client->lastUID);
            }
            
            sprintf(&pref[strlen(pref)], "%c\r%c\r%c\r", 
                (proxy->flags & MAILPROXY_FLAG_IMAP)? '1': '0', 
                (proxy->flags & MAILPROXY_FLAG_LEAVE_MAIL)? '1': '0', 
                (proxy->flags & MAILPROXY_FLAG_SSL)? '1': '0');

            MDBAddValue(pref, accounts);
            
            MemFree(pref);
        } else if (proxy->flags & MAILPROXY_FLAG_ENABLED) {
            LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_BAD_HOSTNAME, LOG_WARNING, 0, proxy->host, client->q->user, 0, 0, NULL, 0);
        }

        proxy = proxy->next;
        continue;
    }

    if (update) {
        // FIXME
        // MsgSetUserSetting(client->q->user, MSGSRV_A_PROXY_LIST, accounts);
    }

    MDBDestroyValueStruct(accounts);

    return;
}

static void 
HandleConnection(void *param)
{
    long threadNumber = (unsigned long)param;
    time_t sleep = time(NULL);
    time_t wokeup;
    MailProxyClient *client;

    if ((client = GetMailProxyClientPoolEntry()) == NULL) {
        XplConsolePrintf("bongocalendar: New worker failed to startup; out of memory.\r\n");

        XplSafeDecrement(MailProxy.client.worker.active);

        return;
    }

    do {
        XplRenameThread(XplGetThreadID(), "MailProxy Worker");

        XplSafeIncrement(MailProxy.client.worker.idle);

        XplWaitOnLocalSemaphore(MailProxy.client.worker.todo);

        XplSafeDecrement(MailProxy.client.worker.idle);

        wokeup = time(NULL);

        XplWaitOnLocalSemaphore(MailProxy.client.semaphore);

        client->q = MailProxy.client.worker.tail;
        if (client->q) {
            MailProxy.client.worker.tail = client->q->previous;
            if (MailProxy.client.worker.tail) {
                MailProxy.client.worker.tail->next = NULL;
            } else {
                MailProxy.client.worker.head = NULL;
            }
        }

        XplSignalLocalSemaphore(MailProxy.client.semaphore);

        if (client->q) {
            ProxyUser(client);
        }

        FreeClientData(client);

        /* Live or die? */
        if (threadNumber == XplSafeRead(MailProxy.client.worker.active)) {
            if ((wokeup - sleep) > MailProxy.client.sleepTime) {
                break;
            }
        }

        sleep = time(NULL);

        MailProxyClientAllocCB(client, NULL);
    } while (MailProxy.state == MAILPROXY_STATE_RUNNING);

    FreeClientData(client);

    MailProxyClientFree(client);

    XplSafeDecrement(MailProxy.client.worker.active);

    XplExitThread(TSR_THREAD, 0);

    return;
}

static int 
ProxyServer(void *ignored)
{
    int i;
    int ccode;
    int runTime;
    int currentTime;
    unsigned long used;
    const unsigned char *user;
    const unsigned char *ptr;
    XplThreadID id;
    Connection *nmap;
    MDBEnumStruct *es;
    MDBValueStruct *accounts = NULL;
    MailProxyQueue *queue = NULL;
    TraceDestination *traceDestination;

    XplSafeIncrement(MailProxy.server.active);

    XplRenameThread(XplGetThreadID(), "Proxy Server");

    traceDestination = CONN_TRACE_CREATE_DESTINATION(CONN_TYPE_NMAP);
    do {
        if ((nmap = NMAPConnectEx(MailProxy.nmap.address, NULL, traceDestination)) != NULL) {
            break;
        }

        XplDelay(1000);
    } while (MailProxy.state == MAILPROXY_STATE_RUNNING);

    if (nmap) {
        NMAPQuit(nmap);
        ConnFree(nmap);
        nmap = NULL;
    }

    CONN_TRACE_FREE_DESTINATION(traceDestination);

    es = MDBCreateEnumStruct(MailProxy.contexts);

    while (MailProxy.state == MAILPROXY_STATE_RUNNING) {
        XplRWWriteLockAcquire(&MailProxy.lock);
        runTime = time(NULL) + MailProxy.runInterval;
        XplRWWriteLockRelease(&MailProxy.lock);

        for (used = 0; (used < MailProxy.contexts->Used) && (MailProxy.state == MAILPROXY_STATE_RUNNING); used++) {
            if (!queue) {
                queue = (MailProxyQueue *)MemMalloc(sizeof(MailProxyQueue));
            }

            while (queue 
                    && (MailProxy.state == MAILPROXY_STATE_RUNNING) 
                    && ((user = MDBEnumerateObjectsEx(MailProxy.contexts->Value[used], C_USER, NULL, FALSE, es, MailProxy.contexts)) != NULL)) {
                if (accounts) {
                    MDBFreeValues(accounts);
                } else {
                    accounts = MDBShareContext(MailProxy.contexts);
                }

                // FIXME
                // if ((MsgGetUserSetting(user, MSGSRV_A_MESSAGING_DISABLED, accounts) == 0) || (accounts->Value[accounts->Used - 1][0] != '1')) {
                if (accounts->Value[accounts->Used - 1][0] != '1') {
                    MDBFreeValues(accounts);

                    // FIXME
                    // if (MsgGetUserFeature(user, FEATURE_PROXY, MSGSRV_A_PROXY_LIST, accounts)) {
                        ptr = strrchr(user, '\\');
                        if (ptr) {
                            strcpy(queue->user, ptr + 1);
                        } else {
                            strcpy(queue->user, user);
                        }

                        strcpy(queue->context, MailProxy.contexts->Value[used]);

                        queue->accounts = accounts;
                        accounts = NULL;


                        QUEUE_WORK_TO_DO(queue, id, ccode);
                        if (!ccode) {
                            XplSignalLocalSemaphore(MailProxy.client.worker.todo);

                            while ((XplSafeRead(MailProxy.client.worker.active) > MailProxy.max.threadsParallel) 
                                    && (MailProxy.state == MAILPROXY_STATE_RUNNING)) {
                                XplDelay(500);
                            }

                            queue = (MailProxyQueue *)MemMalloc(sizeof(MailProxyQueue));
                            continue;
                        }

                        LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, queue->user, sizeof(MailProxyClient), __LINE__, NULL, 0);
                    // }

                    if (accounts) {
                        MDBFreeValues(accounts);
                    }
                }
            }
        }

        currentTime = time(NULL);

        while ((currentTime < runTime) && (MailProxy.state == MAILPROXY_STATE_RUNNING) && !MailProxy.force) {
            currentTime++;
            XplDelay(1000);
        }

        MailProxy.force = FALSE;
    }

    /*    Shutting down    */
    MailProxy.state = MAILPROXY_STATE_UNLOADING;

#if VERBOSE
    XplConsolePrintf("bongomailprox: Shutting down.\r\n");
#endif

    id = XplSetThreadGroupID(MailProxy.id.group);

    if (MailProxy.nmap.ssl.enable) {
        MailProxy.nmap.ssl.enable = FALSE;

        if (MailProxy.nmap.ssl.context) {
            ConnSSLContextFree(MailProxy.nmap.ssl.context);
            MailProxy.nmap.ssl.context = NULL;
        }
    }

    ConnCloseAll(1);

    for (i = 0; (XplSafeRead(MailProxy.server.active) > 1) && (i < 60); i++) {
        XplDelay(1000);
    }

    XplWaitOnLocalSemaphore(MailProxy.client.semaphore);

    ccode = XplSafeRead(MailProxy.client.worker.idle);
    while (ccode--) {
        XplSignalLocalSemaphore(MailProxy.client.worker.todo);
    }

    XplSignalLocalSemaphore(MailProxy.client.semaphore);

    for (i = 0; XplSafeRead(MailProxy.client.worker.active) && (i < 60); i++) {
        XplDelay(1000);
    }

    if (XplSafeRead(MailProxy.server.active) > 1) {
        XplConsolePrintf("bongomailprox: %d server threads outstanding; attempting forceful unload.\r\n", XplSafeRead(MailProxy.server.active) - 1);
    }

    if (XplSafeRead(MailProxy.client.worker.active)) {
        XplConsolePrintf("bongomailprox: %d threads outstanding; attempting forceful unload.\r\n", XplSafeRead(MailProxy.client.worker.active));
    }

    if (es) {
        MDBDestroyEnumStruct(es, MailProxy.contexts);
    }

    if (accounts) {
        MDBDestroyValueStruct(accounts);
    }

    if (MailProxy.contexts) {
        MDBDestroyValueStruct(MailProxy.contexts);
    }

    if (queue) {
        MemFree(queue);
    }

    XplRWLockDestroy(&MailProxy.lock);

    XplCloseLocalSemaphore(MailProxy.client.worker.todo);
    XplCloseLocalSemaphore(MailProxy.client.semaphore);

    MsgShutdown();

    if (MailProxy.client.ssl.enable && MailProxy.client.ssl.context) {
        ConnSSLContextFree(MailProxy.client.ssl.context);
        MailProxy.client.ssl.context = NULL;
    }

    CONN_TRACE_SHUTDOWN();
    ConnShutdown();

    LoggerClose(MailProxy.handle.logging);
    MailProxy.handle.logging = NULL;

    MemPrivatePoolFree(MailProxy.client.pool);
    MemoryManagerClose(MSGSRV_AGENT_PROXY);

#if VERBOSE
    XplConsolePrintf("bongomailprox: Shutdown complete\r\n");
#endif

    XplSignalLocalSemaphore(MailProxy.sem.main);
    XplWaitOnLocalSemaphore(MailProxy.sem.shutdown);

    XplCloseLocalSemaphore(MailProxy.sem.shutdown);
    XplCloseLocalSemaphore(MailProxy.sem.main);

    XplSetThreadGroupID(id);

    return(0);
}

// MailProxy.contexts
static BongoConfigItem ProxyConfig[] = {
	{ BONGO_JSON_STRING, "o:postmaster/s", &MailProxy.postmaster },
	{ BONGO_JSON_INT, "o:interval/i", &MailProxy.runInterval }, // in seconds
	{ BONGO_JSON_INT, "o:max_threads/i", &MailProxy.max.threadsParallel },
	{ BONGO_JSON_INT, "o:max_accounts/i", &MailProxy.max.accounts },
	{ BONGO_JSON_STRING, "o:queue/s", &MailProxy.nmap.address },
	{ BONGO_JSON_NULL, NULL, NULL }
};

static BOOL
ReadConfiguration(void)
{
    if (! MsgGetServerCredential(MailProxy.nmap.hash))
        return FALSE;

    if (! ReadBongoConfiguration(ProxyConfig, "mailproxy"))
        return FALSE;

    return(TRUE);
}

XplServiceCode(SignalHandler)

int
XplServiceMain(int argc, char *argv[])
{
    int                ccode;
    XplThreadID        id;

    XplSignalHandler(SignalHandler);
    XplInit();

    MailProxy.force = FALSE;
    MailProxy.runInterval = 3600 * 3;
    MailProxy.contexts = NULL;
    MailProxy.handle.logging = NULL;
    MailProxy.handle.directory = NULL;
    MailProxy.max.accounts = 3;
    MailProxy.max.threadsParallel = 6;
    MailProxy.client.pool = NULL;
    strcpy(MailProxy.nmap.address, "127.0.0.1");

    XplSafeWrite(MailProxy.server.active, 0);
    XplSafeWrite(MailProxy.client.worker.active, 0);
    XplSafeWrite(MailProxy.client.worker.maximum, 16);

    MailProxy.id.group = XplGetThreadGroupID();

    if (MemoryManagerOpen(MSGSRV_AGENT_PROXY) == TRUE) {
        MailProxy.client.pool = MemPrivatePoolAlloc("Proxy Connections", sizeof(MailProxyClient), 0, 3072, TRUE, FALSE, MailProxyClientAllocCB, NULL, NULL);
        if (MailProxy.client.pool != NULL) {
            XplOpenLocalSemaphore(MailProxy.sem.main, 0);
            XplOpenLocalSemaphore(MailProxy.sem.shutdown, 1);
            XplOpenLocalSemaphore(MailProxy.client.semaphore, 1);
            XplOpenLocalSemaphore(MailProxy.client.worker.todo, 1);
        } else {
            MemoryManagerClose(MSGSRV_AGENT_PROXY);

            XplConsolePrintf("bongomailprox: Unable to create connection pool; shutting down.\r\n");
            return(-1);
        }
    } else {
        XplConsolePrintf("bongomailprox: Unable to initialize memory manager; shutting down.\r\n");
        return(-1);
    }

    ConnStartup(CONNECTION_TIMEOUT, TRUE);

    MDBInit();
    MailProxy.handle.directory = (MDBHandle)MsgInit();
    if (MailProxy.handle.directory == NULL) {
        XplBell();
        XplConsolePrintf("bongomailprox: Invalid directory credentials; exiting!\r\n");
        XplBell();

        MemoryManagerClose(MSGSRV_AGENT_PROXY);

        return(-1);
    }

    NMAPInitialize();

    MailProxy.handle.logging = LoggerOpen("bongomailprox");
    if (MailProxy.handle.logging == NULL) {
        XplConsolePrintf("bongomailprox: Unable to initialize Nsure Audit.  Logging disabled.\r\n");
    }

    XplRWLockInit(&MailProxy.lock);

    MailProxy.state = MAILPROXY_STATE_STOPPING;
    if (ReadConfiguration()) {
        CONN_TRACE_INIT((char *)MsgGetWorkDir(NULL), "mailprox");
        // CONN_TRACE_SET_FLAGS(CONN_TRACE_ALL); /* uncomment this line and pass '--enable-conntrace' to autogen to get the agent to trace all connections */

        MailProxy.client.ssl.config.certificate.file = MsgGetTLSCertPath(NULL);
        MailProxy.client.ssl.config.key.type = GNUTLS_X509_FMT_PEM;
        MailProxy.client.ssl.config.key.file = MsgGetTLSKeyPath(NULL);

        MailProxy.client.ssl.context = ConnSSLContextAlloc(&(MailProxy.client.ssl.config));
        if (MailProxy.client.ssl.context) {
            MailProxy.client.ssl.enable = TRUE;
        }

        NMAPSetEncryption(MailProxy.client.ssl.context);

        if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {
            XplConsolePrintf("bongocalendar: Could not drop to unprivileged user '%s', exiting.\r\n", MsgGetUnprivilegedUser());
        } else {
            MailProxy.state = MAILPROXY_STATE_RUNNING;
        }
    } else {
        LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NOT_CONFIGURED, LOG_ERROR, 0, "Proxy NMAP Servers", NULL, 0, 0, NULL, 0);
    }

    XplStartMainThread(PRODUCT_SHORT_NAME, &id, ProxyServer, 16384, NULL, ccode);

    XplUnloadApp(XplGetThreadID());
    return(0);
}
