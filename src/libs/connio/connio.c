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

#include <connio.h>
#include <stdio.h>

#ifdef HAVE_SYS_SOCKET_H
#include <sys/socket.h>

#ifndef AF_LOCAL
#  ifdef AF_UNIX
#    define AF_LOCAL AF_UNIX
#  else
#    error "no AF_UNIX macro for local domain sockets"
#  endif
#endif

#ifndef PF_LOCAL
#  ifdef PF_UNIX
#    define PF_LOCAL PF_UNIX
#  else
#    error "no PF_UNIX macro for local domain sockets"
#  endif
#endif

#endif

#ifdef HAVE_NETINET_IN_H
#include <netinet/in.h>
#endif

#include <sys/un.h>

#include "conniop.h"

ConnIOGlobals ConnIO;
gnutls_dh_params_t dh_params;
gnutls_rsa_params_t rsa_params;

gnutls_session_t
__gnutls_new(bongo_ssl_context *context, gnutls_connection_end_t con_end) {
    gnutls_session_t session;
    int ccode;

    ccode = gnutls_init(&session, con_end);
    if (ccode) {
        return(NULL);
    }

    if (con_end == GNUTLS_SERVER) {
        ccode = gnutls_set_default_export_priority(session);

        /* store in the credetials loaded earler */
        ccode = gnutls_credentials_set(session, GNUTLS_CRD_CERTIFICATE, context->cert_cred);

        /* initialize the dh bits */
        gnutls_dh_set_prime_bits(session, 1024);
    } else {
        gnutls_certificate_credentials_t xcred = NULL;

        const int cert_type_priority[4] = { GNUTLS_CRT_X509, GNUTLS_CRT_OPENPGP, 0 };

        /* defaults are ok here */
        gnutls_set_default_priority (session);

        /* store the priority for x509 or openpgp out there
         * i doubt that openpgp will be used but perhaps there is a server that supports it */
        gnutls_certificate_type_set_priority (session, cert_type_priority);
        gnutls_certificate_allocate_credentials (&xcred);
        gnutls_certificate_set_x509_trust_file (xcred, XPL_DEFAULT_CERT_PATH, GNUTLS_X509_FMT_PEM);

        /* set the empty credentials in the session */
        gnutls_credentials_set (session, GNUTLS_CRD_CERTIFICATE, xcred);
    }

    return session;
}

BOOL 
ConnStartup(unsigned long TimeOut, BOOL EnableSSL)
{
    /**
     * Start up the connio library - must be initialised before use
     */
    int i;
    unsigned char c;
    unsigned char seed[129];
    FILE *genparams;

    /* initialize the gnutls library */
    gnutls_global_init();

    /* try to load dh data out of a file */
    gnutls_dh_params_init(&dh_params);
    genparams = fopen(XPL_DEFAULT_DHPARAMS_PATH, "r");
    if (genparams) {
        char tmpdata[2048];
        gnutls_datum dh_parameters;

        dh_parameters.size = fread(tmpdata, 1, sizeof(tmpdata)-1, genparams);
        /* null the last byte */
        tmpdata[dh_parameters.size] = 0x00;

        fclose(genparams);

        /* store the data in the datum so that we can initialize it */
        dh_parameters.data = tmpdata;

        /* import the key */
        i = gnutls_dh_params_import_pkcs3(dh_params, &dh_parameters, GNUTLS_X509_FMT_PEM);
    } else {
        /* generate dh stuff */
        gnutls_dh_params_generate2(dh_params, 1024);
    }

    /* try to load the rsa data out of a file */
    gnutls_rsa_params_init(&rsa_params);
    genparams = fopen(XPL_DEFAULT_RSAPARAMS_PATH, "r");
    if (genparams) {
        char tmpdata[2048];
        gnutls_datum rsa_parameters;

        rsa_parameters.size = fread(tmpdata, 1, sizeof(tmpdata)-1, genparams);
        /* null the last byte */
        tmpdata[rsa_parameters.size] = 0x00;

        fclose(genparams);

        /* store the data in the datum so that we can initialize it */
        rsa_parameters.data = tmpdata;

        /* import the key */
        i = gnutls_rsa_params_import_pkcs1(rsa_params, &rsa_parameters, GNUTLS_X509_FMT_PEM);
    } else {
        /* generate rsa stuff */
        gnutls_rsa_params_generate2(rsa_params, 512);
    }

    XplOpenLocalSemaphore(ConnIO.allocated.sem, 0);

    ConnIO.timeOut = TimeOut;
    ConnIO.allocated.head = NULL;
    ConnIO.encryption.enabled = FALSE;
    ConnIO.trace.enabled = FALSE;

    IPInit();

    if (EnableSSL && (ConnIO.encryption.enabled == FALSE)) {
        ConnIO.encryption.enabled = TRUE;

        srand((unsigned int)time(NULL));

        memset(seed, 0, sizeof(seed));
        for (i = 0; i < 64; i++) {
            c = ' ' + ((unsigned char)rand() % 0x60) ;
            sprintf(seed + (i * 2), "%02x", c);
        }

        seed[sizeof(seed) - 1] = '\0';
    }

    XplSignalLocalSemaphore(ConnIO.allocated.sem);

    return(TRUE);
}

void 
ConnSSLContextFree(bongo_ssl_context *context)
{
    if (context) {
        gnutls_certificate_free_credentials(context->cert_cred);
        MemFree(context);
    }

    return;
}

bongo_ssl_context * 
ConnSSLContextAlloc(ConnSSLConfiguration *ConfigSSL)
{
    bongo_ssl_context *result;
    int ccode;

    /* get me the context */
    result = MemMalloc(sizeof(bongo_ssl_context));

    /* allocate the credential holder */
    ccode = gnutls_certificate_allocate_credentials(&(result->cert_cred));
    if (ccode) {
        MemFree(result);
        return(NULL);
    }

    ccode = gnutls_certificate_set_x509_key_file(result->cert_cred, ConfigSSL->certificate.file, ConfigSSL->key.file, ConfigSSL->key.type);

    /* store both the rsa and dh data in the creds */
    gnutls_certificate_set_dh_params(result->cert_cred, dh_params);
    gnutls_certificate_set_rsa_export_params(result->cert_cred, rsa_params);

    return result;
}

Connection *
ConnAlloc(BOOL Buffers)
{
    register Connection *c;

    c = (Connection *)MemMalloc(sizeof(Connection));
    if (c) {
        memset(c, 0, sizeof(Connection));

        c->socket = -1;

        if (Buffers) {
            c->receive.buffer = (char *)MemMalloc(CONN_TCP_MTU + 1);
            if (c->receive.buffer) {
                c->send.buffer = (char *)MemMalloc(CONN_TCP_MTU + 1);
                if (c->send.buffer) {
                    c->receive.read = c->receive.write = c->receive.buffer;
                    c->receive.write[0] = '\0';
                    c->receive.remaining = CONN_TCP_MTU;
                    c->receive.timeOut = ConnIO.timeOut;

                    c->send.read = c->send.write = c->send.buffer;
                    c->send.write[0] = '\0';
                    c->send.remaining = CONN_TCP_MTU;
                    c->send.timeOut = ConnIO.timeOut;
                } else {
                    MemFree(c->receive.buffer);
                    MemFree(c);

                    return(NULL);
                }
            } else {
                MemFree(c);

                return(NULL);
            }
        } else {
            c->receive.timeOut = ConnIO.timeOut;

            c->send.timeOut = ConnIO.timeOut;
        }

        XplWaitOnLocalSemaphore(ConnIO.allocated.sem);
        c->allocated.previous = NULL;
        if ((c->allocated.next = ConnIO.allocated.head) != NULL) {
            c->allocated.next->allocated.previous = c;
        }
        ConnIO.allocated.head = c;
        XplSignalLocalSemaphore(ConnIO.allocated.sem);
    }

    return(c);
}

IPSOCKET 
ConnSocket(Connection *conn)
{
    conn->socket = socket(PF_INET, SOCK_STREAM, IPPROTO_TCP);

    return(conn->socket);
}

IPSOCKET 
ConnServerSocket(Connection *conn, int backlog)
{
    int ccode;

    conn->socket = IPsocket(PF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (conn->socket != -1) {
        ccode = 1;
        setsockopt(conn->socket, SOL_SOCKET, SO_REUSEADDR, (unsigned char *)&ccode, sizeof(ccode));

        ccode = IPbind(conn->socket, (struct sockaddr *)&(conn->socketAddress), sizeof(conn->socketAddress));
        if (ccode != -1) {
            ccode = IPlisten(conn->socket, backlog);
        }

        if (ccode != -1) {
            ccode = sizeof(conn->socketAddress);
            IPgetsockname(conn->socket, (struct sockaddr *)&(conn->socketAddress), &ccode);
        } else {
            IPclose(conn->socket);
            conn->socket = -1;
        }
    }

    return(conn->socket);
}

IPSOCKET 
ConnServerSocketUnix(Connection *conn, const char *path, int backlog)
{
    int ccode;

    conn->socket = IPsocket(PF_LOCAL, SOCK_STREAM, 0);
    if (conn->socket != -1) {
        struct sockaddr_un addr;
        memset(&addr, 0, sizeof(addr));
        addr.sun_family = AF_LOCAL;
        strncpy(addr.sun_path, path, sizeof(addr.sun_path) - 1);

        unlink(path);
        ccode = IPbind(conn->socket, (struct sockaddr *)&(addr), sizeof(addr));
        if (ccode != -1) {
            ccode = IPlisten(conn->socket, backlog);
        } else {
            IPclose(conn->socket);
            conn->socket = -1;
        }
#if 0
        if (ccode != -1) {
            ccode = sizeof(conn->socketAddress);
            IPgetsockname(conn->socket, (struct sockaddr *)&(conn->socketAddress), &ccode);
        } else {
            IPclose(conn->socket);
            conn->socket = -1;
        }            
#endif
    }

    return(conn->socket);
}

__inline static IPSOCKET 
ConnConnectInternal(Connection *conn, struct sockaddr *saddr, socklen_t slen, bongo_ssl_context *context, TraceDestination *destination, unsigned long timeOut)
{
    int ccode;

    conn->socket = IPsocket(PF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (conn->socket != -1) {
        if (!saddr) {
            ccode = 0;
        } else {
            ccode = IPbind(conn->socket, (struct sockaddr *)saddr, slen);
        }

        if (ccode != -1) {
            if (timeOut == 0) {
                ccode = IPconnect(conn->socket, (struct sockaddr *)&(conn->socketAddress), sizeof(conn->socketAddress));
            } else {
                ccode = XplIPConnectWithTimeout(conn->socket, (struct sockaddr *)&(conn->socketAddress), sizeof(conn->socketAddress), timeOut);
            }
        }

        CONN_TRACE_BEGIN(conn, CONN_TYPE_OUTBOUND, destination);
        CONN_TRACE_EVENT(conn, CONN_TRACE_EVENT_CONNECT);
        if (ccode != -1) {
            if (!context) {
                ccode = 1;
                setsockopt(conn->socket, IPPROTO_TCP, 1, (unsigned char *)&ccode, sizeof(ccode));

                conn->ssl.enable = FALSE;

                return(conn->socket);
            } else if ((conn->ssl.context = __gnutls_new(context, GNUTLS_CLIENT)) != NULL) {
                setsockopt(conn->socket, IPPROTO_TCP, 1, (unsigned char *)&ccode, sizeof(ccode));
                
                gnutls_transport_set_ptr (conn->ssl.context, (gnutls_transport_ptr_t) conn->socket);
                ccode = gnutls_handshake (conn->ssl.context);
                if (!ccode) {
                    CONN_TRACE_EVENT(conn, CONN_TRACE_EVENT_SSL_CONNECT);
                    conn->ssl.enable = TRUE;

                    return(conn->socket);
                }
                CONN_TRACE_ERROR(conn, "SSL_CONNECT", ccode);
            }
        } else {
            CONN_TRACE_ERROR(conn, "CONNECT", ccode);
        }

        CONN_TRACE_EVENT(conn, CONN_TRACE_EVENT_CLOSE);
        CONN_TRACE_END(conn);
        IPclose(conn->socket);
        conn->socket = -1;
    }

    return(-1);
}

IPSOCKET 
ConnConnect(Connection *conn, struct sockaddr *saddr, socklen_t slen, bongo_ssl_context *context)
{
    return(ConnConnectInternal(conn, saddr,slen, context, NULL, 0));
}

IPSOCKET 
ConnConnectWithTimeOut(Connection *conn, struct sockaddr *saddr, socklen_t slen, bongo_ssl_context *context, TraceDestination *destination, unsigned long timeOut)
{
    return(ConnConnectInternal(conn, saddr,slen, context, destination, timeOut));
}

IPSOCKET 
ConnConnectEx(Connection *conn, struct sockaddr *saddr, socklen_t slen, bongo_ssl_context *context, TraceDestination *destination)
{
    return(ConnConnectInternal(conn, saddr,slen, context, destination, 0));
}

int 
ConnEncrypt(Connection *conn, bongo_ssl_context *context)
{
    int ccode;
    register Connection *c = conn;

    if ((c->ssl.context = __gnutls_new(context, GNUTLS_CLIENT)) != NULL) {
        setsockopt(c->socket, IPPROTO_TCP, 1, (unsigned char *)&ccode, sizeof(ccode));

        gnutls_transport_set_ptr(c->ssl.context, (gnutls_transport_ptr_t) c->socket);
        ccode = gnutls_handshake (c->ssl.context);
        if (!ccode) {
            CONN_TRACE_EVENT(c, CONN_TRACE_EVENT_SSL_CONNECT);
            c->ssl.enable = TRUE;
            return(0);
        }

        gnutls_deinit(c->ssl.context);
        c->ssl.context = NULL;
    }
    c->ssl.enable = FALSE;

    return(-1);
}

BOOL 
ConnNegotiate(Connection *conn, bongo_ssl_context *context)
{
    int ccode;
    register Connection *c = conn;

    if (c->ssl.enable == FALSE) {
        ccode = 1;
        setsockopt(c->socket, IPPROTO_TCP, 1, (unsigned char *)&ccode, sizeof(ccode));

        return(TRUE);
    }
    
    if ((c->ssl.context = __gnutls_new(context, GNUTLS_SERVER)) != NULL) {
        setsockopt(c->socket, IPPROTO_TCP, 1, (unsigned char *)&ccode, sizeof(ccode));

        gnutls_transport_set_ptr(c->ssl.context, (gnutls_transport_ptr_t) c->socket);
        ccode = gnutls_handshake(c->ssl.context);
        if (!ccode) {
            CONN_TRACE_EVENT(c, CONN_TRACE_EVENT_SSL_CONNECT);
            return(TRUE);
        }
        gnutls_deinit(c->ssl.context);
        c->ssl.enable = FALSE;
        c->ssl.context = NULL;

    }

    return(FALSE);
}

int 
ConnClose(Connection *Conn, unsigned long Flags)
{
    register Connection *c = Conn;

    CONN_TRACE_EVENT(Conn, CONN_TRACE_EVENT_CLOSE);
    CONN_TRACE_END(c);
    
    ConnTcpClose(c);

    return(0);
}

void 
ConnCloseAll(unsigned long Flags)
{
    register Connection *c;

    XplWaitOnLocalSemaphore(ConnIO.allocated.sem);

    c = ConnIO.allocated.head;
    while (c) {
        if (!c->bSelfManage) {
            ConnTcpClose(c);
        }

        c = c->allocated.next;
    }

    XplSignalLocalSemaphore(ConnIO.allocated.sem);

    return;
}

void 
ConnFree(Connection *Conn)
{
    register Connection *c = Conn;

    if (!c) {
        return;
    }

    if (c->socket != -1) {
        ConnTcpClose(c);
    }

    if (c->receive.buffer) {
        MemFree(c->receive.buffer);
    }

    if (c->send.buffer) {
        MemFree(c->send.buffer);
    }

    if (c->ssl.context) {
        gnutls_deinit(c->ssl.context);
    }

    XplWaitOnLocalSemaphore(ConnIO.allocated.sem);
    if (c->allocated.next) {
        c->allocated.next->allocated.previous = c->allocated.previous;
    }
    if (c->allocated.previous) {
        c->allocated.previous->allocated.next = c->allocated.next;
    } else {
        ConnIO.allocated.head = c->allocated.next;
    }
    XplSignalLocalSemaphore(ConnIO.allocated.sem);

    MemFree(c);

    return;
}

void 
ConnShutdown(void)
{
    register Connection *c;
    register Connection *cNext;

    XplWaitOnLocalSemaphore(ConnIO.allocated.sem);

    c = ConnIO.allocated.head;
    ConnIO.allocated.head = NULL;

    while (c != NULL) {
        ConnTcpClose(c);

        if (c->receive.buffer) {
            MemFree(c->receive.buffer);
        }

        if (c->send.buffer) {
            MemFree(c->send.buffer);
        }

        cNext = c->allocated.next;

        MemFree(c);

        c = cNext;
    }

    if (ConnIO.encryption.enabled) {
        ConnIO.encryption.enabled = FALSE;
    }

    XplCloseLocalSemaphore(ConnIO.allocated.sem);

    return;
}

int ConnAccept(Connection *Server, Connection **Client)
{
    int length;
    Connection *c = ConnAlloc(TRUE);

    if (c) {
        length = sizeof(c->socketAddress);
        c->socket = accept(Server->socket, (struct sockaddr *)&(c->socketAddress), &length);
        CONN_TRACE_BEGIN(c, CONN_TYPE_INBOUND, NULL);
        if (c->socket != -1) {
            *Client = c;
            CONN_TRACE_EVENT(c, CONN_TRACE_EVENT_CONNECT);
            return(c->socket);
        }
        CONN_TRACE_ERROR(c, "ACCEPT", -1);
        CONN_TRACE_END(c);
        ConnFree(c);
    }

    return(-1);
}

/*
    Non-buffered direct network functions
*/
int 
ConnSend(Connection *Conn, char *Buffer, unsigned int Length)
{
    int count;

    ConnTcpWrite(Conn, Buffer, Length, &count);

    return(count);
}

int 
ConnReceive(Connection *Conn, char *Buffer, unsigned int Length)
{
    int count;

    ConnTcpRead(Conn, Buffer, Length, &count);

    return(count);
}

/*
    Buffered network access functions
*/

/* ConnRead() blocks until any amount of data is available.  */
/* It behaves much like a blocking recv(). */  
/* It returns the number of bytes read which can be anything */
/* between 1 and Length on success.  The argument Length is  */
/* usually the size of buffer DEST. */

int 
ConnRead(Connection *Conn, char *Dest, int Length)
{
    int read;
    Connection *c = Conn;

    read = c->receive.write - c->receive.read;
    do {
        if (read > 0) {
            if (read <= Length) {
                memcpy(Dest, c->receive.read, read);

                c->receive.read = c->receive.write = c->receive.buffer;
                c->receive.remaining = CONN_TCP_MTU;

                c->receive.write[0] = '\0';
                return(read);
            }

            memcpy(Dest, c->receive.read, Length);

            c->receive.read += Length;

            return(Length);
        }

        ConnTcpRead(c, c->receive.buffer, CONN_TCP_MTU, &read);
        if (read > 0) {
            c->receive.read = c->receive.buffer;
            c->receive.write = c->receive.buffer + read;
            c->receive.remaining = CONN_TCP_MTU - read;

            c->receive.write[0] = '\0';
            continue;
        } else if (read == 0) {
            return(0);
        }

        break;
    } while (TRUE);

    return(-1);
}

/* ConnReadCount() blocks until Count bytes have been received */
/* and copied into buffer Dest.  It returns the number of      */
/* bytes read which can only be Count on success.              */

int 
ConnReadCount(Connection *Conn, char *Dest, int Count)
{
    size_t buffered;
    size_t remaining = Count;
    char *d = Dest;
    Connection *c = Conn;

    while (remaining > 0) {
        buffered = c->receive.write - c->receive.read;
        if (buffered > 0) {
            if (remaining <= buffered) {
                memcpy(d, c->receive.read, remaining);

                c->receive.read += remaining;
                if (c->receive.read == c->receive.write) {
                    c->receive.read = c->receive.write = c->receive.buffer;
                    c->receive.remaining = CONN_TCP_MTU;

                    c->receive.write[0] = '\0';
                }

                return(Count);
            }

            memcpy(d, c->receive.read, buffered);

            d += buffered;
            remaining -= buffered;

            c->receive.read = c->receive.write = c->receive.buffer;
            c->receive.remaining = CONN_TCP_MTU;

            c->receive.write[0] = '\0';
        }

        if (remaining < CONN_TCP_MTU) {
            ConnTcpRead(c, c->receive.write, c->receive.remaining, &buffered);

            if (buffered > 0) {
                c->receive.write += buffered;
                c->receive.remaining -= buffered;

                c->receive.write[0] = '\0';
                continue;
            }
        } else {
            do {
                ConnTcpRead(c, d, remaining, &buffered);
                if (buffered > 0) {
                    d += buffered;
                    remaining -= buffered;

                    *d = '\0';
                    continue;
                }

                return(-1);
            } while (remaining > CONN_TCP_MTU);

            continue;
        }

        return(-1);
    }

    return(Count);
}

int 
ConnReadLine(Connection *Conn, char *Line, int Length)
{
    int count;
    char *cur;
    char *limit;
    char *dest;
    Connection *c = Conn;

    if (Length > 0) {
        if (Line != NULL) {
            dest = Line;

            cur = c->receive.read;
            limit = c->receive.write;
        } else {
            return(0);
        }
    } else {
        return(0);
    }

    do {
        *limit = '\0';
        while (cur < limit) {
            if ((cur = strchr(cur, '\n')) != NULL) {
                cur++;
                count = cur - c->receive.read;
                if (dest + count < Line + Length) {
                    memcpy(dest, c->receive.read, count);
                    dest += count;

                    c->receive.read = cur;
                    if (c->receive.read == c->receive.write) {
                        c->receive.read = c->receive.write = c->receive.buffer;
                        c->receive.remaining = CONN_TCP_MTU;

                        c->receive.write[0] = '\0';
                    }

                    *dest = '\0';

                    return(dest - Line);
                }

                count = Length - (dest - Line);

                memcpy(dest, c->receive.read, count);

                c->receive.read += count;
                if (c->receive.read == c->receive.write) {
                    c->receive.read = c->receive.write = c->receive.buffer;
                    c->receive.remaining = CONN_TCP_MTU;

                    c->receive.write[0] = '\0';
                }

                return(Length);
            }

            cur = limit;
            break;
        }

        count = cur - c->receive.read;
        if (count > 0) {
            if (dest + count < Line + Length) {
                memcpy(dest, c->receive.read, count);
                dest += count;

                c->receive.read = c->receive.write = c->receive.buffer;
                c->receive.remaining = CONN_TCP_MTU;

                c->receive.write[0] = '\0';
            } else {
                count = Length - (dest - Line);

                memcpy(dest, c->receive.read, count);

                c->receive.read += count;
                if (c->receive.read == c->receive.write) {
                    c->receive.read = c->receive.write = c->receive.buffer;
                    c->receive.remaining = CONN_TCP_MTU;

                    c->receive.write[0] = '\0';
                }

                return(Length);
            }
        }

        ConnTcpRead(c, c->receive.buffer, CONN_TCP_MTU, &count);
        if (count > 0) {
            cur = c->receive.read = c->receive.buffer;
            limit = c->receive.write = cur + count;
            c->receive.remaining = CONN_TCP_MTU - count;

            c->receive.write[0] = '\0';
            continue;
        }

        return(-1);
    } while (TRUE);

    return(dest - Line);
}


int 
ConnReadAnswer(Connection *Conn, char *Line, int Length)
{
    int count;
    char *cur;
    char *limit;
    char *dest;
    Connection *c = Conn;

    if (Length > 0) {
        if (Line != NULL) {
            dest = Line;

            cur = c->receive.read;
            limit = c->receive.write;
        } else {
            return(0);
        }
    } else {
        return(0);
    }

    do {
        *limit = '\0';
        while (cur < limit) {
            if ((cur = strchr(cur, '\n')) != NULL) {
                count = cur - c->receive.read;
                if (count > 0) {
                    if (dest + count < Line + Length) {
                        memcpy(dest, c->receive.read, count);
                        dest += count;

                        c->receive.read = cur + 1;
                        if (c->receive.read == c->receive.write) {
                            c->receive.read = c->receive.write = c->receive.buffer;
                            c->receive.remaining = CONN_TCP_MTU;

                            c->receive.write[0] = '\0';
                        }

                        if (dest[-1] == '\r') {
                            dest--;
                            count--;
                        }

                        *dest = '\0';

                        return(dest - Line);
                    }

                    count = Length - (dest - Line);

                    memcpy(dest, c->receive.read, count);

                    c->receive.read += count;
                    if (c->receive.read == c->receive.write) {
                        c->receive.read = c->receive.write = c->receive.buffer;
                        c->receive.remaining = CONN_TCP_MTU;

                        c->receive.write[0] = '\0';
                    }

                    return(Length);
                }

                c->receive.read = ++cur;
                if (c->receive.read == c->receive.write) {
                    c->receive.read = c->receive.write = c->receive.buffer;
                    c->receive.remaining = CONN_TCP_MTU;

                    c->receive.write[0] = '\0';
                }

                if ((dest > Line) && (dest[-1] == '\r')) {
                    dest--;
                }

                *dest = '\0';

                return(dest - Line);
            }

            cur = limit;
            break;
        }

        count = cur - c->receive.read;
        if (count > 0) {
            if (dest + count < Line + Length) {
                memcpy(dest, c->receive.read, count);
                dest += count;

                c->receive.read = c->receive.write = c->receive.buffer;
                c->receive.remaining = CONN_TCP_MTU;

                c->receive.write[0] = '\0';
            } else {
                count = Length - (dest - Line);

                memcpy(dest, c->receive.read, count);

                c->receive.read += count;
                if (c->receive.read == c->receive.write) {
                    c->receive.read = c->receive.write = c->receive.buffer;
                    c->receive.remaining = CONN_TCP_MTU;

                    c->receive.write[0] = '\0';
                }

                return(Length);
            }
        }

        ConnTcpRead(c, c->receive.buffer, CONN_TCP_MTU, &count);
        if (count > 0) {
            cur = c->receive.read = c->receive.buffer;
            limit = c->receive.write = cur + count;
            c->receive.remaining = CONN_TCP_MTU - count;

            c->receive.write[0] = '\0';
            continue;
        }

        return(-1);
    } while (TRUE);

    return(dest - Line);
}

/* ConnReadToAllocatedBuffer() is designed to read the */
/* next full line regardless of its length. */
/* The buffer pointer passed to this function must be */
/* NULL or point to an existing allocated buffer. */
/* This function will reallocate the buffer if */
/* the line is bigger than the current buffer. */
/* If a line is so big that the system can not allocate */
/* a buffer big enough to hold it, this function will: */
/* - read and discard everything up to the next newline, */
/* - free the buffer, */
/* - set the buffer pointer to NULL */
long 
ConnReadToAllocatedBuffer(Connection *c, char **buffer, unsigned long *bufferSize)
{
    long tmpSize;
    char *tmpBuffer;
    int count;
    char *cur;
    char *limit;
    char *dest;

    if (*buffer) {
        ;
    } else {
        *buffer = MemMalloc(CONN_BUFSIZE);
        if (*buffer == NULL) {
            return(CONN_ERROR_MEMORY);
        }
        *bufferSize = CONN_BUFSIZE;
    }

    dest = *buffer;
    cur = c->receive.read;
    limit = c->receive.write;

    for(;;) {
        *limit = '\0';
        if (cur < limit) {
            if ((cur = strchr(cur, '\n')) != NULL) {
                count = cur - c->receive.read;
                if (count > 0) {
                    if ((dest + count) < (*buffer + *bufferSize)) {
                        ;
                    } else {
                        tmpSize = count + (dest - *buffer);
                        tmpBuffer = MemRealloc(*buffer, tmpSize);
                        if (!tmpBuffer) {
                            /* flush the rest of the line */
                            c->receive.read = cur + 1;
                            if (c->receive.read == c->receive.write) {
                                c->receive.read = c->receive.write = c->receive.buffer;
                                c->receive.remaining = CONN_TCP_MTU;
                                c->receive.write[0] = '\0';
                            }
                            
                            /* free the buffer */
                            MemFree(*buffer);
                            *buffer = NULL;
                            return(CONN_ERROR_MEMORY);
                        }

                        dest = tmpBuffer + (dest - *buffer);
                        *buffer = tmpBuffer;
                        *bufferSize = tmpSize;
                    }

                    memcpy(dest, c->receive.read, count);
                    dest += count;

                    c->receive.read = cur + 1;
                    if (c->receive.read == c->receive.write) {
                        c->receive.read = c->receive.write = c->receive.buffer;
                        c->receive.remaining = CONN_TCP_MTU;

                        c->receive.write[0] = '\0';
                    }

                    if (dest[-1] == '\r') {
                        dest--;
                        count--;
                    }

                    *dest = '\0';

                    return(dest - *buffer);
                }

                c->receive.read = ++cur;
                if (c->receive.read == c->receive.write) {
                    c->receive.read = c->receive.write = c->receive.buffer;
                    c->receive.remaining = CONN_TCP_MTU;

                    c->receive.write[0] = '\0';
                }

                if ((dest > *buffer) && (dest[-1] == '\r')) {
                    dest--;
                }

                *dest = '\0';

                return(dest - *buffer);
            }

            cur = limit;
        }

        count = cur - c->receive.read;
        if (count > 0) {
            if ((dest + count) < (*buffer + *bufferSize)) {
                ;
            } else {
                tmpSize = max((unsigned long)(count + (dest - *buffer)) << 1, (*bufferSize) << 1);
                tmpBuffer = MemRealloc(*buffer, tmpSize);
                if (!tmpBuffer) {
                    /* flush the rest of the line */
                    for(;;) {
                        if ((cur = strchr(cur, '\n')) != NULL) {
                            c->receive.read = cur + 1;
                            if (c->receive.read == c->receive.write) {
                                c->receive.read = c->receive.write = c->receive.buffer;
                                c->receive.remaining = CONN_TCP_MTU;
                                c->receive.write[0] = '\0';
                            }
                            break;
                        }
                        
                        c->receive.read = c->receive.write = c->receive.buffer;
                        c->receive.remaining = CONN_TCP_MTU;
                        c->receive.write[0] = '\0';

                        ConnTcpRead(c, c->receive.buffer, CONN_TCP_MTU, &count);
                        if (count <= 0) {
                            break;
                        }

                        cur = c->receive.read = c->receive.buffer;
                        limit = c->receive.write = cur + count;
                        c->receive.remaining = CONN_TCP_MTU - count;
                        c->receive.write[0] = '\0';
                    }

                    /* free the buffer */
                    MemFree(*buffer);
                    *buffer = NULL;
                    return(CONN_ERROR_MEMORY);
                }

                dest = tmpBuffer + (dest - *buffer);
                *buffer = tmpBuffer;
                *bufferSize = tmpSize;
            }

            memcpy(dest, c->receive.read, count);
            dest += count;

            c->receive.read = c->receive.write = c->receive.buffer;
            c->receive.remaining = CONN_TCP_MTU;

            c->receive.write[0] = '\0';
        } else if (dest == *buffer) {
            /* There is nothing in *buffer or c->receive.buffer, and we are going to the wire. */
            /* There is a good chance that we will block, lets make sure our buffer is not really big */
            if (*bufferSize < (CONN_BUFSIZE + 1)) {
                ;
            } else {
                /* shrink the buffer */
                MemFree(*buffer);
                *buffer = MemMalloc(CONN_BUFSIZE);
                if (*buffer) {
                    *bufferSize = CONN_BUFSIZE;
                } else {
                    *bufferSize = 0;
                    return(CONN_ERROR_MEMORY);
                }
            }
        }

        ConnTcpRead(c, c->receive.buffer, CONN_TCP_MTU, &count);
        if (count <= 0) {
            c->send.remaining = 0;
            c->send.read = c->send.write;
            return(CONN_ERROR_NETWORK);
        }

        cur = c->receive.read = c->receive.buffer;
        limit = c->receive.write = cur + count;
        c->receive.remaining = CONN_TCP_MTU - count;

        c->receive.write[0] = '\0';
    }
}


int 
ConnReadToFile(Connection *Conn, FILE *Dest, int Count)
{
    size_t buffered;
    size_t remaining;
    Connection *c = Conn;

    remaining = Count;
    while (remaining > 0) {
        buffered = c->receive.write - c->receive.read;
        if (buffered > 0) {
            if (remaining <= buffered) {
                if (fwrite(c->receive.read, sizeof(char), remaining, Dest) == remaining) {
                    c->receive.read += remaining;
                    if (c->receive.read == c->receive.write) {
                        c->receive.read = c->receive.write = c->receive.buffer;
                        c->receive.remaining = CONN_TCP_MTU;

                        c->receive.write[0] = '\0';
                    }

                    return(Count);
                }

                return(-1);
            }

            if ((remaining > c->receive.remaining) && (c->receive.remaining < CONN_TCP_THRESHOLD)) {
                if (fwrite(c->receive.read, sizeof(char), buffered, Dest) == buffered) {
                    remaining -= buffered;

                    c->receive.read = c->receive.write = c->receive.buffer;
                    c->receive.remaining = CONN_TCP_MTU;

                    c->receive.write[0] = '\0';
                } else {
                    return(-1);
                }
            }
        }

        ConnTcpRead(c, c->receive.write, c->receive.remaining, &buffered);
        if (buffered > 0) {
            c->receive.write += buffered;
            c->receive.remaining -= buffered;

            c->receive.write[0] = '\0';
            continue;
        }

        return(-1);
    }

    return(Count);
}

int
ConnReadToFileUntilEOS(Connection *Src, FILE *Dest)
{
    int written = 0;
    size_t count;
    char *cur;
    char *limit;
    BOOL finished = FALSE;
    BOOL escaped = FALSE;

    do {
        cur = Src->receive.read;
        limit = Src->receive.write;
        while (cur < limit) {
            if (*cur && (*cur != '\n')) {
                cur++;
                continue;
            }

            if (*cur) {
                if ((limit - cur) > 3) {
                    cur++;
                    
                    if (cur[0] != '.') {
                        continue;
                    }

                    if ((cur[1] == '\r') && (cur[2] == '\n')) {
                        finished = TRUE;
                        break;
                    }

                    if (cur[1] != '.') {
                        continue;
                    }
                    escaped = TRUE;
                }

                break;
            }

            *cur++ = ' ';
            continue;
        }

        count = cur - Src->receive.read;
        if (count) {
            if (fwrite(Src->receive.read, sizeof(unsigned char), count, Dest) == count) {
                written += count;
            } else {
                return(-1);
            }

            if (finished) {
                cur += 3;
                if (cur < limit) {
                    Src->receive.read = cur;
                } else {
                    Src->receive.read = Src->receive.write = Src->receive.buffer;
                    Src->receive.remaining = CONN_TCP_MTU;

                    Src->receive.write[0] = '\0';
                }

                break;
            }

            if (escaped) {
                escaped = FALSE;
                cur++;
                count = limit - cur;
                if (count) {
                    memmove(Src->receive.buffer, cur, count);
                }

                Src->receive.read = Src->receive.buffer;
                Src->receive.write = Src->receive.buffer + count;
                Src->receive.remaining = CONN_TCP_MTU - count;

                Src->receive.write[0] = '\0';
                continue;
            }

            count = limit - cur;
            if (count) {
                memmove(Src->receive.buffer, cur, count);
            }

            Src->receive.read = Src->receive.buffer;
            Src->receive.write = Src->receive.buffer + count;
            Src->receive.remaining = CONN_TCP_MTU - count;

            Src->receive.write[0] = '\0';
        }

        ConnTcpRead(Src, Src->receive.write, Src->receive.remaining, &count);
        if (count > 0) {
            Src->receive.read = Src->receive.buffer;
            Src->receive.write += count;
            Src->receive.remaining -= count;

            Src->receive.write[0] = '\0';
            continue;
        }

        return(-1);
    } while (!finished);

    return(written);
}

int 
ConnReadToConn(Connection *Src, Connection *Dest, int Count)
{
    unsigned int sent;
    size_t buffered;
    size_t remaining;
    Connection *s = Src;
    Connection *d = Dest;

    if ((remaining = Count) > 0) {
        ConnTcpFlush(d, d->send.read, d->send.write, &sent);
        if (sent >= 0) {
            d->send.read = d->send.write = d->send.buffer;
            d->send.remaining = CONN_TCP_MTU;

            d->send.write[0] = '\0';
        } else {
            return(-1);
        }
    } else {
        return(0);
    }

    while (remaining > 0) {
        buffered = s->receive.write - s->receive.read;
        if (buffered > 0) {
            if (remaining <= buffered) {
                ConnTcpWrite(d, s->receive.read, remaining, &sent);
                if (sent == remaining) {
                    s->receive.read += sent;
                    if (s->receive.read == s->receive.write) {
                        s->receive.read = s->receive.write = s->receive.buffer;
                        s->receive.remaining = CONN_TCP_MTU;

                        s->receive.write[0] = '\0';
                    }

                    return(Count);
                }

                return(-1);
            }

            if ((remaining > s->receive.remaining) && (s->receive.remaining < CONN_TCP_THRESHOLD)) {
                ConnTcpWrite(d, s->receive.read, buffered, &sent);
                if (sent == buffered) {
                    remaining -= sent;

                    s->receive.read = s->receive.write = s->receive.buffer;
                    s->receive.remaining = CONN_TCP_MTU;

                    s->receive.write[0] = '\0';
                } else {
                    return(-1);
                }
            }
        }

        ConnTcpRead(s, s->receive.write, s->receive.remaining, &buffered);
        if (buffered > 0) {
            s->receive.write += buffered;
            s->receive.remaining -= buffered;

            s->receive.write[0] = '\0';
            continue;
        }

        return(-1);
    }

    return(Count);
}

int
ConnReadToConnUntilEOS(Connection *Src, Connection *Dest)
{
    int written = 0;
    unsigned int count;
    char *cur;
    char *limit;
    BOOL finished = FALSE;

    ConnTcpFlush(Dest, Dest->send.read, Dest->send.write, &count);
    if (count >= 0) {
        Dest->send.read = Dest->send.write = Dest->send.buffer;
        Dest->send.remaining = CONN_TCP_MTU;

        Dest->send.write[0] = '\0';
    } else {
        return(-1);
    }

    do {
        cur = Src->receive.read;
        limit = Src->receive.write;
        while (cur < limit) {
            if (*cur && (*cur != '\n')) {
                cur++;
                continue;
            }

            if (*cur) {
                if ((limit - cur) > 3) {
                    cur++;

                    if ((cur[0] != '.') || (cur[1] != '\r') || (cur[2] != '\n')) {
                        continue;
                    }

                    finished = TRUE;
                    cur += 3;
                    break;
                }

                break;
            }

            *cur++ = ' ';
            continue;
        }
                                                                                                                                                                            
        if (Src->receive.read < cur) {
            ConnTcpFlush(Dest, Src->receive.read, cur, &count);
            if ((size_t)(cur - Src->receive.read) == count) {
                written += count;

                if (!finished) {
                    count = limit - cur;
                    if (count) {
                        memmove(Src->receive.buffer, cur, count);
                    }

                    Src->receive.read = Src->receive.buffer;
                    Src->receive.write = Src->receive.buffer + count;
                    Src->receive.remaining = CONN_TCP_MTU - count;

                    Src->receive.write[0] = '\0';
                } else {
                    if (cur < limit) {
                        Src->receive.read = cur;
                    } else {
                        Src->receive.read = Src->receive.write = Src->receive.buffer;
                        Src->receive.remaining = CONN_TCP_MTU;

                        Src->receive.write[0] = '\0';
                    }

                    break;
                }
            } else {
                return(-1);
            }
        }

        ConnTcpRead(Src, Src->receive.write, Src->receive.remaining, &count);
        if (count > 0) {
            Src->receive.read = Src->receive.buffer;
            Src->receive.write += count;
            Src->receive.remaining -= count;

            Src->receive.write[0] = '\0';
            continue;
        }

        return(-1);
    } while (!finished);

    return(written);
}

int 
ConnWrite(Connection *Conn, const char *Buffer, int Length)
{
    const char *b;
    int i;
    size_t r;
    Connection *c = Conn;

    b = Buffer;
    r = Length;
    while (r > 0) {
        if (r < c->send.remaining) {
            memcpy(c->send.write, b, r);

            c->send.write += r;
            c->send.remaining -= r;

            c->send.write[0] = '\0';
            break;
        }

        if (c->send.read < c->send.write) {
            memcpy(c->send.write, b, c->send.remaining);
            b += c->send.remaining;
            r -= c->send.remaining;

            c->send.write += c->send.remaining;

            ConnTcpFlush(c, c->send.read, c->send.write, &i);
            if (i > 0) {
                c->send.read = c->send.write = c->send.buffer;
                c->send.remaining = CONN_TCP_MTU;

                c->send.write[0] = '\0';
                continue;
            }

            c->send.remaining = 0;

            return(-1);
        }

        do {
            ConnTcpFlush(c, b, b + r, &i);
            if (i > 0) {
                b += r;
                r -=  r;

                continue;
            }

            c->send.remaining = 0;

            return(-1);
        } while (r > CONN_TCP_THRESHOLD);
    }

    return(Length);
}

int 
ConnWriteFromFile(Connection *Conn, FILE *Source, int Count)
{
    int i;
    size_t n;
    size_t r;
    Connection *c = Conn;

    r = Count;
    while (r > 0) {
        n = min(r, c->send.remaining);

        while ((n > 0) && !feof(Source)) {
            i = fread(c->send.write, sizeof(char), n, Source);
            if (!ferror(Source)) {
                r -= i;
                n -= i;

                c->send.write += i;
                c->send.remaining -= i;

                continue;
            }

            return(-1);
        }

        ConnTcpFlush(c, c->send.read, c->send.write, &i);
        if (i > 0) {
            c->send.read = c->send.write = c->send.buffer;
            c->send.remaining = CONN_TCP_MTU;

            c->send.write[0] = '\0';
            continue;
        }

        return(-1);
    }

    return(Count);
}

int
ConnWriteFile(Connection *Conn, FILE *Source)
{
    int i;
    size_t n;
    size_t r;
    Connection *c = Conn;
    
    n = 0;
    r = c->send.remaining;
    do {
        if (r > CONN_TCP_THRESHOLD) {
            i = fread(c->send.write, sizeof(unsigned char), r, Source);
            if (!ferror(Source)) {
                r -= i;
                c->send.write += i;
                c->send.remaining -= i;
            } else {
                return(-1);
            }
        }
        
        n += c->send.write - c->send.read;
        
        ConnTcpFlush(c, c->send.read, c->send.write, &i);
        if (i > 0) {
            c->send.read = c->send.write = c->send.buffer;
            c->send.remaining = r = CONN_TCP_MTU;

            c->send.write[0] = '\0';
            continue;
        }
        
        return(-1);
    } while (!feof(Source) && !ferror(Source));
    
    return(n);
}

int 
ConnWriteVF(Connection *c, const char *format, va_list ap)
{
    int i;

    do {
        if (CONN_BUFSIZE < c->send.remaining) {
            va_list ap2;
            
            XPL_VA_COPY(ap2, ap);
            i = XplVsnprintf(c->send.write, CONN_BUFSIZE, format, ap);

            if (i > CONN_BUFSIZE) {
                char *tmp = MemMalloc(i);

                if (!tmp) {
                    return -1;
                }

                i = XplVsnprintf(tmp, i, format, ap2);

                i = ConnWrite(c, tmp, i);
                MemFree(tmp);

                va_end(ap2);

                return i;
            } else if (i >= 0) {
                c->send.write += i;
                c->send.remaining -= i;

                c->send.write[0] = '\0';

                va_end(ap2);

                return(i);
            }
            va_end(ap2);
        }

        ConnTcpFlush(c, c->send.read, c->send.write, &i);
        if (i > 0) {
            c->send.read = c->send.write = c->send.buffer;
            c->send.remaining = CONN_TCP_MTU;

            c->send.write[0] = '\0';
            continue;
        }

        return(-1);
    } while (TRUE);    
}

int
ConnWriteF(Connection *conn, const char *Format, ...)
{
    va_list ap;
    int ret;
    
    va_start(ap, Format);
    ret = ConnWriteVF(conn, Format, ap);
    va_end(ap);

    return ret;
}

int 
ConnFlush(Connection *Conn)
{
    int count;
    Connection *c = Conn;

    ConnTcpFlush(c, c->send.read, c->send.write, &count);

    if (count >= 0) {
        c->send.read += count;
        if (c->send.read == c->send.write) {
            c->send.read = c->send.write = c->send.buffer;
            c->send.remaining = CONN_TCP_MTU;

            c->send.write[0] = '\0';
        }
    }

    return(count);
}

int
XplPrintIPAddress(char *buffer, int bufLen, unsigned long address)
{
#ifdef XPL_BIG_ENDIAN
    return snprintf(buffer, bufLen, "%lu.%lu.%lu.%lu", 
		    ((address>>24) & 0xff), 
		    ((address>>16) & 0xff), 
		    ((address>>8) & 0xff), 
		    (address & 0xff));
#else
    return snprintf(buffer, bufLen, "%lu.%lu.%lu.%lu", 
		    (address & 0xff),
		    ((address>>8) & 0xff), 
		    ((address>>16) & 0xff), 	
		    ((address>>24) & 0xff));
#endif
}

