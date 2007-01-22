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

void
FreeFolderList(IMAPFolderPath *folder)
{
    if(folder->next) {
        FreeFolderList(folder->next);
        MemFree(folder->next);
    }
    
    if(folder->folderPath) {
        MemFree(folder->folderPath);
    }
}

ProxyUID *
GetProxyUID(ProxyAccount *proxy, const unsigned char *folderPath)
{
    ProxyUID *uid = &proxy->uidList;
    ProxyUID *parent;
    
    do {
        if (uid->folderPath && !strcasecmp(uid->folderPath, folderPath))
            break;

        parent = uid;            
        uid = uid->next;
    } while (uid);

    
    if (uid == NULL) {
        parent->next = (ProxyUID *)MemMalloc(sizeof(ProxyUID));
        uid = parent->next;
        
        if (uid != NULL) {
            uid->next = NULL;
            uid->uid = 0;
            uid->folderPath = (unsigned char *)MemMalloc(strlen(folderPath) + 1);
            if (uid->folderPath)
                strcpy(uid->folderPath, folderPath);
        }
        
        proxy->uidCount++;
    }
    
    return uid;
}

/* NOTE:
 *
 * The IMAP proxy relies on the IMAP Pipelining capabilities
 * it will issue the fetch(message) request right when getting the response
 * to the fetch(uid) command. Same happens with the delete; we'll queue the
 * commands and after retrieving all the messages, flush and retrieve the
 * response. Read the RFC!
 *
 */
unsigned long 
ProxyIMAPAccount(MailProxyClient *client, ProxyAccount *proxy)
{
    unsigned long retval = -1;
    int count;
    int ccode;
    int messages;
    int remaining;
    long j;
    long len;
    long id;
    long curUID;
    long stopUID;
    long startUID;
    long proxiedBytes = 0;
    long proxiedMessages = 0;
    unsigned char *ptr, *ptr2;
    unsigned char *uid;
    unsigned char tag[8];
    XplDnsRecord *dns = NULL;
    IMAPFolderPath folderList = {NULL, NULL};
    IMAPFolderPath *nextFolder = NULL;
    ProxyUID *lastuid;

    proxy->flags |= MAILPROXY_FLAG_STORE_UID;

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
                    proxy->flags |= MAILPROXY_FLAG_BAD_HOST;
                    LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_BAD_HOSTNAME, LOG_ERROR, 0, proxy->host, client->q->user, 0, 0, NULL, 0);

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
        return(-1);
    }

    /* fixme - this check only works on single server solutions */
    if (MsgGetHostIPAddress() != client->conn->socketAddress.sin_addr.s_addr) {
        if (!(proxy->flags & MAILPROXY_FLAG_SSL)) {
            ccode = ConnConnect(client->conn, NULL, 0, NULL);
        } else if (MailProxy.client.ssl.enable) {
            if (proxy->port == PORT_IMAP) {
                ccode = ConnConnect(client->conn, NULL, 0, NULL);
            } else {
                ccode = ConnConnect(client->conn, NULL, 0, MailProxy.client.ssl.context);
            }
        } else {
            ccode = -1;
        }
    } else {
        /* We don't want to proxy ourselves... */
        proxy->flags |= MAILPROXY_FLAG_BAD_HOST;
        proxy->flags &= ~MAILPROXY_FLAG_STORE_UID;

        LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, client->q->user, NULL, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
        return(-1);
    }

    /* IMAP greeting: * OK ... */
    if (ccode != -1) {
        if (proxy->flags & MAILPROXY_FLAG_SSL) {
            if (proxy->port == PORT_IMAP) {
                if (((ccode = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE)) != -1) 
                        && (client->line[0] == '*') 
                        && ((ccode = ConnWrite(client->conn, "0000 CAPABILITY\r\n", 17)) != -1) 
                        && ((ccode = ConnFlush(client->conn)) != -1) 
                        && ((ccode = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE)) != -1)) {
                    if (client->line[0] == '*') {
                        if (strstr(client->line, "STARTTLS") != NULL) {
                            ccode = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE);
                        } else {
                            ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE);
                            ccode = -1;
                        }
                    } else {
                        ccode = -1;
                    }

                    if ((ccode != -1) 
                            && (XplStrNCaseCmp(client->line, "0000 OK", 7) == 0) 
                            && ((ccode = ConnWrite(client->conn, "0000 STARTTLS\r\n", 15)) != -1) 
                            && ((ccode = ConnFlush(client->conn)) != -1) 
                            && ((ccode = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE)) != -1) 
                            && (XplStrNCaseCmp(client->line, "0000 OK", 7) == 0)) {
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
                        && (XplStrNCaseCmp(client->line, "* OK", 3) != 0)) {
                    ccode = -1;
                }
            }
        } else if (((ccode = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE)) != -1) 
                && (XplStrNCaseCmp(client->line, "* OK", 3) != 0)) {
            ccode = -1;
        }
    } else {
        proxy->flags |= MAILPROXY_FLAG_BAD_HANDSHAKE;
        LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_IP_CONNECT_FAILURE, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
        return(-1);
    }

    if (ccode == -1) {
        proxy->flags |= MAILPROXY_FLAG_BAD_HANDSHAKE;
        LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NEGOTIATION_FAILED, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
        return(-1);
    }

    /* Expected IMAP conversation:
        Bongo: 0000 LOGIN <user> <password>
        IMAP: 0000 OK LOGIN completed 
        
        Bongo: 0001 LIST "" "*"
        IMAP: * LIST (<flags>) "/" "<folder path>"
        IMAP: * LIST (<flags>) "/" "<folder path>"
        IMAP: * LIST (<flags>) "/" "<folder path>"
        IMAP: * LIST (<flags>) "/" "<folder path>"
        IMAP: * LIST (<flags>) "/" "<folder path>"
        IMAP: 0001 OK LIST completed
        
        Bongo: 0002 SELECT <folder path>
        IMAP: 0002 OK SELECT completed
        
        Bongo: 0003 UID FETCH <last uid + 1>:* (UID)
        IMAP: <untagged responses beginning with *>
        IMAP: * <sequence number> FETCH (UID <uid>)
        IMAP: * <sequence number> FETCH (UID <uid>)
        IMAP: * <sequence number> FETCH (UID <uid>)
        IMAP: * <sequence number> FETCH (UID <uid>)
        IMAP: 0003 OK UID FETCH completed
        
        (loop for next folder)
    */
    strcpy(tag, "0000 OK");
    if ((ccode == -1) 
            || ((ccode = ConnWriteF(client->conn, "0000 LOGIN %s %s\r\n", proxy->user, proxy->password)) == -1) 
            || ((ccode = ConnFlush(client->conn)) == -1) 
            || ((ccode = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE)) == -1) 
            || (XplStrNCaseCmp(client->line, tag, 7) != 0)) { 
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

    if (proxy->flags & MAILPROXY_FLAG_ALLFOLDERS) {
        if (((ccode = ConnWrite(client->conn, "0001 LIST \"\" \"*\"\r\n", 18)) != -1) 
                && ((ccode = ConnFlush(client->conn)) != -1)) {
            while ((ccode != -1) 
                    && ((count = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE)) != -1) 
                    && (client->line[0] == '*') 
                    && ((ptr = strchr(client->line, '\"')) != 0)) {
                if ((ptr = strchr(ptr + 3, '\"')) != 0) {
                    if (nextFolder) {
                        nextFolder->next = (IMAPFolderPath *)MemMalloc(sizeof(IMAPFolderPath));
                        if (nextFolder->next) {
                            nextFolder = nextFolder->next;
                            nextFolder->next = 0;
                        } else {
                            LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), __LINE__, NULL, 0);
                            goto end;
                        }
                    } else {
                        nextFolder = &folderList;
                    }
    
                    ptr++; /* step past first quote and knock off last quote*/                
                    if ((ptr2 = strchr(ptr, '\"')) != 0) {
                        ptr2[0] = 0;
                    } else {
                        proxy->flags |= MAILPROXY_FLAG_BAD_HANDSHAKE;
                        LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NETWORK_IO_FAILURE, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), __LINE__, NULL, 0);
                        goto end;
                    }
                    
                    nextFolder->folderPath = (unsigned char *)MemMalloc(strlen(ptr) + 1);
                    if (nextFolder->folderPath) {
                        strcpy(nextFolder->folderPath, ptr);
                    } else {
                        LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), __LINE__, NULL, 0);
                        goto end;
                    }
                } else {
                    proxy->flags |= MAILPROXY_FLAG_BAD_HANDSHAKE;
                    LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NETWORK_IO_FAILURE, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), __LINE__, NULL, 0);
                    goto end;
                }
            }
    
            tag[3] = '1';
            if ((ccode == -1) || (XplStrNCaseCmp(client->line, tag, 7) != 0)) {
                proxy->flags |= MAILPROXY_FLAG_BAD_HANDSHAKE;
    
                LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NETWORK_IO_FAILURE, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), __LINE__, NULL, 0);
                goto end;
            }
        } else {
            proxy->flags |= MAILPROXY_FLAG_BAD_HANDSHAKE;
            LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NETWORK_IO_FAILURE, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), __LINE__, NULL, 0);
    
            return(-1); /* nothing to free */
        }
    } else {
        folderList.folderPath = (unsigned char *)MemMalloc(6);
        if (folderList.folderPath) {
            strcpy(folderList.folderPath, "INBOX");
        } else {
            LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), __LINE__, NULL, 0);
            goto end;
        }
    }

    nextFolder = (folderList.folderPath != NULL) ? &folderList : NULL;
    
    while (nextFolder) {
        lastuid = GetProxyUID(proxy, nextFolder->folderPath);
        startUID = stopUID = ((lastuid != NULL) ? lastuid->uid : 0) + 1;
        
        count = strlen(nextFolder->folderPath);
        
        if (((ccode = ConnWrite(client->conn, "0002 SELECT \"", 13)) != -1) 
                && ((ccode = ConnWrite(client->conn, nextFolder->folderPath, count)) != -1)
                && ((ccode = ConnWrite(client->conn, "\"\r\n", 3)) != -1)
                && ((ccode = ConnFlush(client->conn)) != -1)) {
            /* replace spaces with 0x7f for NMAP */
            for (j = 0; j < count; j++) {
                if (nextFolder->folderPath[j] == ' ')
                    nextFolder->folderPath[j] = 0x7f;
            }
            
            do {
                ccode = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE);
                if (ccode != -1) {
                    continue;
                }
    
                proxy->flags |= MAILPROXY_FLAG_BAD_HANDSHAKE;
    
                LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NETWORK_IO_FAILURE, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), __LINE__, NULL, 0);
                goto end;
            } while (client->line[0] == '*');
    
            tag[3] = '2';
        } else {
            proxy->flags |= MAILPROXY_FLAG_BAD_HANDSHAKE;
            LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NETWORK_IO_FAILURE, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), __LINE__, NULL, 0);
    
            goto end;
        }
    
        if (XplStrNCaseCmp(client->line, tag, 7) == 0) {
            tag[3] = '3';
    
            messages = 0;
            remaining = 0;
    
            if ((ccode = ConnWriteF(client->conn, "0003 UID FETCH %lu:* (UID)\r\n", startUID)) != -1) {
                ccode = ConnFlush(client->conn);
            }
        } else {
            proxy->flags |= MAILPROXY_FLAG_BAD_HANDSHAKE;
            LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NETWORK_IO_FAILURE, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), __LINE__, NULL, 0);
            goto end;
        }
         
        while ((ccode != -1) 
                && ((count = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE)) != -1) 
                && (client->line[0] == '*') 
                && (sscanf(client->line, "* %lu FETCH", &id) == 1)) {
            uid = NULL;
    
            /* * <sequence number> FETCH (UID <uid>) */
            ptr = strchr(client->line, '(');
            if (ptr) {
                ptr++;
    
                do {
                    while (*ptr && isspace(*ptr)) {
                        ptr++;
                    }
    
                    switch (toupper(*ptr)) {
                        case 'U': {
                            if ((toupper(ptr[1]) == 'I')
                                    && (toupper(ptr[2]) == 'D')) {
                                ptr += 3;
    
                                while (*ptr && isspace(*ptr)) {
                                    ptr++;
                                }
    
                                uid = ptr;
                                ptr = strchr(ptr, ')');
                                if (ptr) {
                                    *ptr++ = '\0';
                                    break;
                                }
                            }
    
                            uid = NULL;
                            ptr = NULL;
                            break;
                        }
    
                        default: {
                            uid = NULL;
                            ptr = NULL;
                            break;
                        }
                    }
                } while (ptr && (ptr[0] != '\0'));
            }
    
            if ((uid) && ((curUID = atol(uid)) >= startUID)) {
                if (messages) {
                    ccode = ConnWriteF(client->conn, ",%lu", id);
                } else {
                    ccode = ConnWriteF(client->conn, "0004 FETCH %lu", id);
                }
    
                if (ccode != -1) {
                    if (curUID > stopUID) {
                        stopUID = curUID;
                    }
    
                    messages++;
                }
            }
    
            continue;
        }
    
        if (XplStrNCaseCmp(client->line, tag, 7) == 0) {
            tag[3] = '4';
        } else {
            proxy->flags |= MAILPROXY_FLAG_BAD_HANDSHAKE;
            LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NETWORK_IO_FAILURE, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), __LINE__, NULL, 0);
            goto end;
        }
    
        if ((ccode != -1) 
                && (messages) 
                && ((ccode = ConnWrite(client->conn, " (BODY.PEEK[])\r\n", 16)) != -1) 
                && ((ccode = ConnFlush(client->conn)) != -1)) {
            client->nmap = NMAPConnectEx(MailProxy.nmap.address, NULL, client->conn->trace.destination);
        } else if (ccode != -1) {
            /* We've already got all the messages, or there are none */
            LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_PROXY_USER, LOG_WARNING, 0, client->q->user, proxy->host, 0, 0, NULL, 0);
            nextFolder = nextFolder->next;
            continue;
        } else {
            proxy->flags |= MAILPROXY_FLAG_BAD_PROXY;
            LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NETWORK_IO_FAILURE, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), __LINE__, NULL, 0);
            goto end;
        }
    
        if ((client->nmap) && (NMAPAuthenticate(client->nmap, client->line, CONN_BUFSIZE) == TRUE)) {
            messages = 0;
        } else {
            if (client->nmap) {
                NMAPSendCommand(client->nmap, "QUIT\r\n", 6);
                NMAPReadAnswer(client->nmap, client->line, CONN_BUFSIZE, FALSE);
                LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NMAP_UNAVAILABLE, LOG_ERROR, 0, NULL, NULL, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
            } else {
                LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, client->q->user, sizeof(Connection), __LINE__, NULL, 0);
            }
    
            proxy->flags |= MAILPROXY_FLAG_BAD_PROXY;
            goto end;
        }
    
        while ((ccode != -1) 
                && ((count = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE)) != -1) 
                && (client->line[0] == '*') 
                && (sscanf(client->line, "* %lu FETCH", &id) == 1)) {
            ptr = client->line + count - 1;
            if (*ptr == '}') {
                *ptr-- = '\0';
                while (ptr > client->line) {
                    if (*ptr != '{') {
                        ptr--;
                        continue;
                    }
    
                    *ptr++ = '\0';
                    break;
                }
    
                if (*ptr) {
                    while (ptr && *ptr && isspace(*ptr)) {
                        ptr++;
                    }
                }
            } else {
                ptr = NULL;
            }
    
            if (ptr && *ptr) {
                count = atol(ptr);
            } else {
                proxy->flags |= MAILPROXY_FLAG_BAD_PROXY;
                LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NEGOTIATION_FAILED, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
                goto end;
            }
    
            ccode = NMAPSendCommandF(client->nmap, "QCREA\r\nQSTOR FLAGS 128\r\nQSTOR FROM %s %s -\r\nQSTOR RAW M%s %s %d %s\r\nQSTOR MESSAGE %lu\r\n", MailProxy.postmaster, MailProxy.postmaster, client->q->user, client->q->user, NO_FORWARD, nextFolder->folderPath, count);
            for (j = 0; (ccode != -1) && (j < 4); j++) {
                /* QSTOR does not have an immediate result [it comes once count bytes are sent] */
                if ((ccode = NMAPReadAnswer(client->nmap, client->line, CONN_BUFSIZE, FALSE)) != -1) {
                    continue;
                }
    
                proxy->flags |= MAILPROXY_FLAG_BAD_PROXY;
    
                LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, client->q->user, "Create queue entry", ccode, 0, NULL, 0);
                XplConsolePrintf("bongomailprox: Could not create queue entry, NMAP error %d, user %s", ccode, client->q->user);
    
                goto end;
            }
    
            if (((ccode = ConnReadToConn(client->conn, client->nmap, count)) != -1) 
                    && ((ccode = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE)) != -1) 
                    && (client->line[0] == ')')) {
                proxiedMessages++;
                proxiedBytes += count;
    
                if (((ccode = NMAPReadAnswer(client->nmap, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                        && ((ccode = NMAPSendCommand(client->nmap, "QRUN\r\n", 6)) != -1)) {
                    if ((ccode = NMAPReadAnswer(client->nmap, client->line, CONN_BUFSIZE, TRUE)) == NMAP_ENTRY_CREATED) {
                        if (!(proxy->flags & MAILPROXY_FLAG_LEAVE_MAIL)) {
                            if (messages) {
                                ccode = ConnWriteF(client->conn, ",%lu", id);
                            } else {
                                ccode = ConnWriteF(client->conn, "0005 STORE %lu", id);
                            }
    
                            if (ccode != -1) {
                                messages++;
                            }
                        }
                    }
                }
            } else {
                NMAPSendCommand(client->nmap, "QABRT\r\n", 7);
            }
        }
    
        if (XplStrNCaseCmp(client->line, tag, 7) != 0) {
            proxy->flags |= MAILPROXY_FLAG_BAD_HANDSHAKE;
            LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NEGOTIATION_FAILED, LOG_ERROR, 0, proxy->host, client->q->user, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
            ConnFree(client->conn);
            client->conn = NULL;
            goto end;
        }
    
        if (ccode != -1) {
            if (lastuid != NULL)
                lastuid->uid = stopUID;

            if (messages && (!(proxy->flags & MAILPROXY_FLAG_LEAVE_MAIL)) 
                    && ((ccode = ConnWrite(client->conn, " +FLAGS.SILENT (\\Deleted)\r\n", 27)) != -1) 
                    && ((ccode = ConnFlush(client->conn)) != -1)) {

                ccode = ConnReadAnswer(client->conn, client->line, CONN_BUFSIZE);
            }
        }
        
        nextFolder = nextFolder->next;
    }
    
    if (ccode != -1) {
        proxiedBytes = (proxiedBytes + 1023) / 1024;

        LoggerEvent(MailProxy.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_PROXY_USER, LOG_WARNING, 0, client->q->user, proxy->host, proxiedMessages, proxiedBytes, NULL, 0);

        XplSafeAdd(MailProxy.stats.messages, proxiedMessages);
        XplSafeAdd(MailProxy.stats.kiloBytes, proxiedBytes);
    }
    
    retval = proxiedMessages;

end:
    FreeFolderList(&folderList);

    return(proxiedMessages);
}
