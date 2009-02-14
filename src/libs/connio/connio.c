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

BOOL
__gnutls_new(Connection *conn, bongo_ssl_context *context, gnutls_connection_end_t con_end) {
    int ccode;

    conn->ssl.context = NULL;
    conn->ssl.credentials = NULL;

    ccode = gnutls_init(&conn->ssl.context, con_end);
    if (ccode) {
        return FALSE;
    }

    if (con_end == GNUTLS_SERVER) {
        ccode = gnutls_set_default_export_priority(conn->ssl.context);

        /* store in the credetials loaded earler */
        ccode = gnutls_credentials_set(conn->ssl.context, GNUTLS_CRD_CERTIFICATE, context->cert_cred);

        /* initialize the dh bits */
        gnutls_dh_set_prime_bits(conn->ssl.context, 1024);
    } else {
        const int cert_type_priority[4] = { GNUTLS_CRT_X509, GNUTLS_CRT_OPENPGP, 0 };

        /* defaults are ok here */
        gnutls_set_default_priority (conn->ssl.context);

        /* store the priority for x509 or openpgp out there
         * i doubt that openpgp will be used but perhaps there is a server that supports it */
        gnutls_certificate_type_set_priority (conn->ssl.context, cert_type_priority);
        gnutls_certificate_allocate_credentials (&conn->ssl.credentials);
        gnutls_certificate_set_x509_trust_file (conn->ssl.credentials, XPL_DEFAULT_CERT_PATH, GNUTLS_X509_FMT_PEM);

        /* set the empty credentials in the session */
        gnutls_credentials_set (conn->ssl.context, GNUTLS_CRD_CERTIFICATE, conn->ssl.credentials);
    }

    return TRUE;
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
            } else if (__gnutls_new(conn, context, GNUTLS_CLIENT) != FALSE) {
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

    if (__gnutls_new(c, context, GNUTLS_CLIENT) != FALSE) {
        setsockopt(c->socket, IPPROTO_TCP, 1, (unsigned char *)&ccode, sizeof(ccode));

        gnutls_transport_set_ptr(c->ssl.context, (gnutls_transport_ptr_t) c->socket);
        ccode = gnutls_handshake (c->ssl.context);
        if (!ccode) {
            CONN_TRACE_EVENT(c, CONN_TRACE_EVENT_SSL_CONNECT);
            c->ssl.enable = TRUE;
            return(0);
        }

        gnutls_deinit(c->ssl.context);
        gnutls_certificate_free_credentials(c->ssl.credentials);
        c->ssl.context = NULL;
        c->ssl.credentials = NULL;
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
    
    if (__gnutls_new(conn, context, GNUTLS_SERVER) != FALSE) {
        setsockopt(c->socket, IPPROTO_TCP, 1, (unsigned char *)&ccode, sizeof(ccode));

        gnutls_transport_set_ptr(c->ssl.context, (gnutls_transport_ptr_t) c->socket);
        ccode = gnutls_handshake(c->ssl.context);
        if (!ccode) {
            CONN_TRACE_EVENT(c, CONN_TRACE_EVENT_SSL_CONNECT);
            return(TRUE);
        }
        gnutls_deinit(c->ssl.context);
        gnutls_certificate_free_credentials(c->ssl.credentials);
        c->ssl.enable = FALSE;
        c->ssl.context = NULL;
        c->ssl.credentials = NULL;
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

    if (c->ssl.credentials) {
        gnutls_certificate_free_credentials(c->ssl.credentials);
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

/**
 * Append bytes to a buffer, allocating the buffer and/or resizing it as necessary.
 * If enough space cannot be allocated, the buffer is destroyed, and further calls
 * to this function appear to succeed. 
 * \param	source	Where to copy bytes from
 * \param	size	Number of bytes to copy
 * \param	buffer	Pointer to the buffer (may point to NULL if you wish the buffer to be allocated)
 * \param	start_of_buffer	How many bytes are already in the buffer
 * \param	buffersize	How big the buffer is. 
 * \return			The number of bytes copied from the source into the buffer
 */
long
ConnAppendToAllocatedBuffer(const char *source, const long size, char **buffer,
	unsigned long start_of_buffer, unsigned long *buffersize)
{
    if (size < 0) {
        //there should be no way to copy less than 0 bytes right?
        //this should also allow me to cast below with little worry
        return size;
    }

	// do we need to allocate this buffer first?
	if (*buffer == NULL) {
		// buffer is not allocated
		if (start_of_buffer != 0) {
			// we've already discarded this buffer, so don't reallocate
			return size;
		}
		*buffer = MemMalloc(size);
		*buffersize = size;
	}
	
	// is the buffer big enough to copy in the new data?
	if ((*buffersize - start_of_buffer) < (unsigned long)size) {
		char *new_buffer;
		long new_size;
		
		new_size = start_of_buffer + size;
		new_buffer = MemRealloc(*buffer, new_size);
		if (new_buffer == NULL) {
			// unable to allocate memory, so destroy the whole buffer
			if (*buffer != NULL) MemFree(*buffer);
			*buffer = NULL; 
			*buffersize = 0;
			return CONN_ERROR_MEMORY;
		}
		*buffer = new_buffer;
		*buffersize = new_size;
	}
	
	// we have enough space to append, so copy the memory
	memcpy(*buffer + start_of_buffer, source, size);
	
	return size;
}

/** 
 * Read a line (until CRLF) from a network socket, potentially blocking until enough
 * data is available. This line is read to a buffer (which may be pre-allocated).
 * The buffer may be resized, and if enough space is not available will be destroyed.
 * If the buffer is destroyed (*buffer == NULL), the number of bytes consumed 
 * from the network socket is still returned but the data discarded.
 * The trailing CRLF on the line is consumed.
 * \param	c		Connection to read the line from
 * \param	buffer	Pointer to the allocated buffer (point to NULL if you want the buffer to be allocated for you)
 * \param	bufferSize	Size of the pre-allocated buffer (0 if you want the buffer to be pre-allocated)
 * \return	Number of bytes placed into the buffer.
 */
long
ConnReadToAllocatedBuffer(Connection *c, char **buffer, unsigned long *bufferSize)
{
	BOOL found_end_of_line = FALSE;
	char *end_of_line;
	long data_consumed = 0;
	
	while (found_end_of_line == FALSE) {
		if (c->receive.read == c->receive.write) {
			// we need to fetch more data since the buffers are empty - this blocks
			int count;
			
			ConnTcpRead(c, c->receive.buffer, CONN_TCP_MTU, &count);
			if (count <= 0) {
				c->send.remaining = CONN_TCP_MTU;
				c->send.read = c->send.write;
				return(CONN_ERROR_NETWORK);
			} else {
				c->receive.read = c->receive.buffer;
				c->receive.write = c->receive.buffer + count;
				c->receive.remaining = CONN_TCP_MTU - count;
			}
		}
		
		// look through the buffer for an end of line
		for (end_of_line = c->receive.read; end_of_line < c->receive.write; end_of_line++) {
			if (*end_of_line == '\r') {
				end_of_line++;
				if (*end_of_line == '\n') end_of_line++;
				found_end_of_line = TRUE;
				break;
			}
		}
		
		if (found_end_of_line == FALSE) {
			// didn't find the end of the line
			long BytesCopied = ConnAppendToAllocatedBuffer(c->receive.read, 
				(c->receive.write - c->receive.read), buffer, data_consumed, bufferSize);
			c->receive.read += BytesCopied;
            data_consumed += BytesCopied;
		}
	}
	
	// end of the line has been found, so consume the data to that point
	data_consumed += ConnAppendToAllocatedBuffer(c->receive.read, 
			(end_of_line - c->receive.read), buffer, data_consumed, bufferSize);
	c->receive.read = end_of_line;
	
	// now want to chomp the carriage return off the end of the line
	end_of_line = *buffer + data_consumed - 1;
	while ((data_consumed > 0) && ((*end_of_line == '\n') || (*end_of_line == '\r'))) {
		*end_of_line = '\0';
		end_of_line--;
		data_consumed--;
	}
	
	// reset connection
	c->receive.remaining = CONN_TCP_MTU - (c->receive.write - c->receive.read);
	if (c->receive.remaining == CONN_TCP_MTU) {
		c->receive.read = c->receive.write = c->receive.buffer;
	}
	
	return data_consumed;
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
    int count;
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
            if ((cur - Src->receive.read) == count) {
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

