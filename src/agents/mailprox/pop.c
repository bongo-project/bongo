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
#include <bongoutil.h>
#include <logger.h>
#include <nmlib.h>
#include <mdb.h>
#include <xplresolve.h>

#include "mailprox.h"

unsigned long 
ProxyPOP3Account(MailProxyClient *client, ProxyAccount *proxy)
{
    long i;
    long j;
    long len;
    long ccode;
    long id;
    long first;
    long total;
    long mCount;
    long mSize;
    unsigned char *ptr;
    unsigned char *uid;
    XplDnsRecord *dns = NULL;

    proxy->flags |= MAILPROXY_FLAG_STORE_UID;
    strcpy(client->lastUID, proxy->uid);

    len = strlen(proxy->host) - 1;
    if (proxy->host[len] == '.') {
        proxy->host[len] = '\0';
    }

    if ((client->conn = ConnAlloc(TRUE)) != NULL) {
        client->conn->socketAddress.sin_addr.s_addr = inet_addr(proxy->host);
        if (client->conn->socketAddress.sin_addr.s_addr == INADDR_NONE) {
            ptr = strchr(proxy->host, '@');
            if (ptr) {
                ptr++;
            } else {
                ptr = proxy->host;
            }

            ccode = XplDnsResolve(ptr, &dns, XPL_RR_A);
            switch (ccode) {
                case XPL_DNS_SUCCESS: {
                    client->conn->socketAddress.sin_addr = dns->A.addr;
                    MemFree(dns);
                    break;
                }

                case XPL_DNS_BADHOSTNAME:
                case XPL_DNS_NORECORDS: {
                    LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_BAD_HOSTNAME, LOG_DEBUG, 0, ptr, client->q->user, 0, 0, NULL, 0);
                    proxy->flags |= MAILPROXY_FLAG_BAD_HOST;

                    MemFree(dns);
                    return(-1);
                }

                default:
                case XPL_DNS_FAIL:
                case XPL_DNS_TIMEOUT: {
                    MemFree(dns);

                    LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, client->q->user, sizeof(Connection), __LINE__, NULL, 0);
                    return(-1);
                }
            }
        }

        client->conn->socketAddress.sin_family = AF_INET;
        client->conn->socketAddress.sin_port = htons(proxy->port);
    } else {
        LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, client->q->user, sizeof(Connection), __LINE__, NULL, 0);
        proxy->flags &= ~MAILPROXY_FLAG_STORE_UID;
        return(-1);
    }

    /* fixme - this check only works on single server solutions */
    if (MsgGetHostIPAddress() != client->conn->socketAddress.sin_addr.s_addr) {
        if (!(proxy->flags & MAILPROXY_FLAG_SSL)) {
            ccode = ConnConnect(client->conn, NULL, 0, NULL);
        } else if (MailProxy.client.ssl.enable) {
            if (proxy->port == PORT_POP3) {
                ccode = ConnConnect(client->conn, NULL, 0, NULL);
            } else {
                ccode = ConnConnect(client->conn, NULL, 0, MailProxy.client.ssl.context);
            }
        } else {
            ccode = -1;
        }
    } else {
        /* We don't want to proxy ourselves... */
        LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, client->q->user, NULL, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
        proxy->flags |= MAILPROXY_FLAG_BAD_HOST;
        return(-1);
    }

    /* POP3 greeting */
    if (ccode != -1) {
        if (proxy->flags & MAILPROXY_FLAG_SSL) {
            if (proxy->port == PORT_POP3) {
                if (((ccode = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE)) != -1) 
                        && (client->line[0] == '+') 
                        && ((ccode = ConnWrite(client->conn, "CAPA\r\n", 6)) != -1) 
                        && ((ccode = ConnFlush(client->conn)) != -1) 
                        && ((ccode = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE)) != -1) 
                        && (client->line[0] == '+')) {
                    proxy->flags &= ~MAILPROXY_FLAG_SSL;

                    while ((ccode != -1) && (client->line[0] != POP_OK)) {
                        ccode = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE);
                        if (ccode != -1) {
                            CHOP_NEWLINE(client->line);

                            if (XplStrCaseCmp(client->line, "STLS") != 0) {
                                continue;
                            }

                            proxy->flags |= MAILPROXY_FLAG_SSL;
                        }
                    }

                    if ((ccode != -1) 
                            && (client->line[0] == '+') 
                            && (proxy->flags & MAILPROXY_FLAG_SSL) 
                            && ((ccode = ConnWrite(client->conn, "STLS\r\n", 6)) != -1) 
                            && ((ccode = ConnFlush(client->conn)) != -1) 
                            && ((ccode = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE)) != -1)) {
                        client->conn->ssl.enable = TRUE;
                        ccode = ConnEncrypt(client->conn, MailProxy.client.ssl.context);
                    } else {
                        ccode = -1;
                    }
                } else {
                    ccode = -1;
                }
            } else {
                client->conn->ssl.enable = TRUE;
                if (((ccode = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE)) != -1) 
                        && (client->line[0] != POP_OK)) {
                    ccode = -1;
                }
            }
        } else if (((ccode = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE)) != -1) 
                && (client->line[0] != POP_OK)) {
            ccode = -1;
        }
    } else {
        proxy->flags |= MAILPROXY_FLAG_BAD_HANDSHAKE;
        LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_IP_CONNECT_FAILURE, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
        return(-1);
    }

    if ((ccode != -1) 
            && ((ccode = ConnWriteF(client->conn, "USER %s\r\n", proxy->user)) != -1) 
            && ((ccode = ConnFlush(client->conn)) != -1) 
            && ((ccode = ConnReadLine(client->conn, client->line, CONN_BUFSIZE)) != -1) 
            && (client->line[0] == POP_OK) 
            && ((ccode = ConnWriteF(client->conn, "PASS %s\r\n", proxy->password)) != -1) 
            && ((ccode = ConnFlush(client->conn)) != -1) 
            && ((ccode = ConnReadLine(client->conn, client->line, CONN_BUFSIZE)) != -1) 
            && (client->line[0] == POP_OK) 
            && ((ccode = ConnWrite(client->conn, "UIDL\r\n", 6)) != -1) 
            && ((ccode = ConnFlush(client->conn)) != -1) 
            && ((ccode = ConnReadLine(client->conn, client->line, CONN_BUFSIZE)) != -1) 
            && (client->line[0] == POP_OK)) {
        first = 1;
        total = 0;
        while (((ccode = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE)) != -1) 
                && (client->line[0] != POP_EOD)) {
            total++;

            id = atol(client->line);
            uid = strchr(client->line, ' ');
            if (uid) {
                while (*uid && isspace(*uid)) {
                    uid++;
                }

                if (strcmp(uid, proxy->uid) == 0) {
                    first = id + 1;
                }
            } else {
                LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NEGOTIATION_FAILED, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
                return(-1);
            }
        }
    } else {
        if (ccode != -1) {
            XplSafeIncrement(MailProxy.stats.wrongPassword);

            proxy->flags |= MAILPROXY_FLAG_BAD_PASSWORD;
            LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_WRONG_PASSWORD, LOG_ERROR, 0, client->q->user, proxy->password, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
        } else {
            proxy->flags |= MAILPROXY_FLAG_BAD_HANDSHAKE;
            LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NETWORK_IO_FAILURE, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), __LINE__, NULL, 0);
        }

        return(-1);
    }

    if (ccode != -1) {
        if (first <= total) {
            client->nmap = NMAPConnectEx(MailProxy.nmap.address, NULL, client->conn->trace.destination);
        } else {
            /* We've already got all the messages, or there are none */
            LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_PROXY_USER, LOG_WARNING, 0, client->q->user, proxy->host, 0, 0, NULL, 0);
            return(0);
        }
    } else {
        proxy->flags |= MAILPROXY_FLAG_BAD_PROXY;
        LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NEGOTIATION_FAILED, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
        return(-1);
    }

    if ((client->nmap) && (NMAPAuthenticate(client->nmap, client->line, CONN_BUFSIZE) == TRUE)) {
        mCount = 0;
        mSize = 0;
    } else {
        if (client->nmap) {
            NMAPSendCommand(client->nmap, "QUIT\r\n", 6);
            NMAPReadAnswer(client->nmap, client->line, CONN_BUFSIZE, FALSE);
            LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NMAP_UNAVAILABLE, LOG_ERROR, 0, NULL, NULL, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
        } else {
            LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, client->q->user, sizeof(Connection), __LINE__, NULL, 0);
        }

        proxy->flags |= MAILPROXY_FLAG_BAD_PROXY;
        return(-1);
    }

    if (client->nmap) {
        for (i = first; (ccode != -1) && (i <= total); i++) {
            ccode = NMAPSendCommandF(client->nmap, "QCREA\r\nQSTOR FLAGS 128\r\nQSTOR FROM %s %s -\r\nQSTOR LOCAL %s %s %d\r\nQSTOR MESSAGE\r\n", MailProxy.postmaster, MailProxy.postmaster, client->q->user, client->q->user, NO_FORWARD);
            for (j = 0; (ccode != -1) && (j < 4); j++) {
                /* QSTOR does not have an immediate result [it comes once the . makes it to the other side] */
                if ((ccode = NMAPReadAnswer(client->nmap, client->line, CONN_BUFSIZE, FALSE)) != -1) {
                    continue;
                }

                LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, client->q->user, "Create queue entry", ccode, 0, NULL, 0);
                XplConsolePrintf("bongomailprox: Could not create queue entry, NMAP error %lu, user %s", ccode, client->q->user);

                return(mCount);
            }

            if (((ccode = ConnWriteF(client->conn, "RETR %lu\r\n", i)) != -1) 
                    && ((ccode = ConnFlush(client->conn)) != -1) 
                    && ((ccode = ConnReadLine(client->conn, client->line, CONN_BUFSIZE)) != -1) 
                    && (client->line[0] == POP_OK)) {
                ccode = ConnReadToConnUntilEOS(client->conn, client->nmap);
            } else {
                LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NEGOTIATION_FAILED, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
                return(mCount);
            }

            if (ccode != -1) {
                if (((ccode = NMAPReadAnswer(client->nmap, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) /* QSTOR MESSAGE */
                        && ((ccode = NMAPSendCommand(client->nmap, "QRUN\r\n", 6)) != -1) 
                        && ((ccode = NMAPReadAnswer(client->nmap, client->line, CONN_BUFSIZE, TRUE)) == NMAP_ENTRY_CREATED)) {
                    if (((ccode = ConnWriteF(client->conn, "UIDL %lu\r\n", i)) != -1) 
                            && ((ccode = ConnFlush(client->conn)) != -1) 
                            && ((ccode = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE)) != -1)
                            && (client->line[0] == POP_OK)) {
                        uid = strchr(client->line+4, ' ');
                        while (*uid && isspace(*uid)) {
                            uid++;
                        }
                        /* Save the UID because this message is now in Bongo */
                        strcpy(client->lastUID, uid);

                        mCount++;
                        mSize += ccode;

                        if (proxy->flags & MAILPROXY_FLAG_LEAVE_MAIL) {
                            continue;
                        }

                        if (((ccode = ConnWriteF(client->conn, "DELE %lu\r\n", i)) != -1) 
                                && ((ccode = ConnFlush(client->conn)) != -1) 
                                && ((ccode = ConnReadLine(client->conn, client->line, CONN_BUFSIZE)) != -1)
                                && (client->line[0] == POP_OK)) {
                            continue;
                        }
                    }
                }
            }

            return(mCount);
        }

        mSize = (mSize + 1023) / 1024;

        LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_PROXY_USER, LOG_WARNING, 0, client->q->user, proxy->host, mCount, mSize, NULL, 0);

        XplSafeAdd(MailProxy.stats.messages, mCount);
        XplSafeAdd(MailProxy.stats.kiloBytes, mSize);

        return(mCount);
    }

    LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, client->q->user, sizeof(Connection), __LINE__, NULL, 0);
    return(-1);
}

