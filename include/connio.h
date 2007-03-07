/* 
 * Copyright (c) 2001 Novell, Inc. All Rights Reserved.
 * Copyright (c) 2007 Alex Hudson [GPLv2+]
 * See the Bongo COPYING file for full details.
 * 
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2 of the GNU General Public
 * License as published by the Free Software Foundation.
 * 
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 * 
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, contact Novell, Inc.
 * 
 * To contact Novell about this file by physical or electronic mail,
 * you may find current contact information at www.novell.com.
 */

#ifndef _BONGO_CONNIO_H
#define _BONGO_CONNIO_H

#include "bongo-config.h"

#include <stdarg.h>
#include <gnutls/openssl.h>
#include <gnutls/gnutls.h>
#include <gcrypt.h>

// from openssl
#define SSL_OP_DONT_INSERT_EMPTY_FRAGMENTS    0x00000800L

#if LIBGNUTLS_VERSION_NUMBER < 0x010200
// limited shim for GNUTLS 1.0 compatibility
typedef gnutls_transport_ptr 		gnutls_transport_ptr_t;
typedef gnutls_dh_params 		gnutls_dh_params_t;
typedef gnutls_rsa_params 		gnutls_rsa_params_t;
typedef gnutls_session 			gnutls_session_t;
typedef gnutls_connection_end 		gnutls_connection_end_t;
typedef gnutls_certificate_credentials 	gnutls_certificate_credentials_t;
#endif

typedef struct {
    gnutls_certificate_credentials_t cert_cred;
} bongo_ssl_context;

#define CONN_BUFSIZE                    1023
#define CONN_TCP_MTU                    (1536 * 3)
#define CONN_TCP_THRESHOLD              256
#define DEFAULT_CONNECTION_TIMEOUT      (15 * 60)

/* CONNIO return values */
#define CONN_ERROR_NETWORK              -1
#define CONN_ERROR_MEMORY               -2

// #define _BONGO_CONN_TRACE   /* this can be defined by passing '--enable-conntrace' to autogen */

#define CONN_TRACE_SESSION_INBOUND      (1 << 0)
#define CONN_TRACE_SESSION_OUTBOUND     (1 << 1)
#define CONN_TRACE_SESSION_NMAP         (1 << 2)
#define CONN_TRACE_EVENT_CONNECT        (1 << 3)
#define CONN_TRACE_EVENT_CLOSE          (1 << 4)
#define CONN_TRACE_EVENT_SSL_CONNECT    (1 << 5)
#define CONN_TRACE_EVENT_SSL_SHUTDOWN   (1 << 6)
#define CONN_TRACE_EVENT_READ           (1 << 7)
#define CONN_TRACE_EVENT_WRITE          (1 << 8)
#define CONN_TRACE_EVENT_ERROR          (1 << 9)


#define CONN_TRACE_ALL                  ((CONN_TRACE_SESSION_INBOUND | CONN_TRACE_SESSION_OUTBOUND | CONN_TRACE_SESSION_NMAP) | (CONN_TRACE_EVENT_CONNECT | CONN_TRACE_EVENT_CLOSE | CONN_TRACE_EVENT_SSL_CONNECT | CONN_TRACE_EVENT_SSL_SHUTDOWN | CONN_TRACE_EVENT_ERROR |CONN_TRACE_EVENT_READ | CONN_TRACE_EVENT_WRITE))

#define CONN_TYPE_INBOUND               0
#define CONN_TYPE_OUTBOUND              1
#define CONN_TYPE_NMAP                  2

#define SSL_USE_CLIENT_CERT             (1 << 0)
#define SSL_ALLOW_SSL2                  (1 << 1)
#define SSL_ALLOW_SSL3                  (1 << 2)
#define SSL_ALLOW_CHAIN                 (1 << 3)
#define SSL_DISABLE_EMPTY_FRAGMENTS     (1 << 4)
#define SSL_DONT_INSERT_EMPTY_FRAGMENTS (1 << 5)

#define CHOP_NEWLINE(s) \
        {   unsigned char *p; p = strchr((s), 0x0A); \
            if (p) { \
                *p = '\0'; \
            } \
            p = strrchr((s), 0x0D); \
            if (p) { \
                *p = '\0'; \
            } \
        }

#define SET_POINTER_TO_VALUE(p,s) \
        {   (p) = (s); \
            while(isspace(*(p))) { \
                (p)++; \
            } \
            if ((*(p) == '=') || (*(p) == ':')) { \
                (p)++; \
            } \
            while(isspace(*(p))) { \
                (p)++; \
            } \
        }


#if defined(_BONGO_LINUX) || defined(S390RH) || defined(SOLARIS)

#include <unistd.h>
#include <sys/poll.h>

#define IPSOCKET int

#define ConnSockOpt(c, o, v, p, l) setsockopt((c)->socket, (o), (v), (p), (l))

#define IPInit() XplGetInterfaceList()
#define IPCleanup() XplDestroyInterfaceList()
#define IPsocket(d, t, p) socket((d), (t), (p))
#define IPaccept(s, a, l) accept((s), (a), (l))
#define IPlisten(s, b) listen((s), (b))
#define IPbind(s, a, l) bind((s), (a), (l))
#define IPconnect(s, a, l) connect((s), (a), (l))
#define IPrecv(s, b, l, f) recv((s), (b), (l), (f))
#define IPsend(s, b, l, f) send((s), (b), (l), (f))
#define IPacceptTO(s, a, l, t) accept((s), (a), (l))
#define IPconnectTO(s, a, l, t) connect((s), (a), (l))
#define IPrecvTO(s, b, l, f, t) recv((s), (b), (l), (f))
#define IPsendTO(s, b, l, f, t) send((s), (b), (l), (f))
#define IPclose(s) close((s))
#define IPshutdown(s, h) shutdown((s), (h))
#define IPgetsockname(s, a, l) getsockname((s), (a), (l))
#define IPgetpeername(s, a, l) getpeername((s), (a), (l))

#define CONN_TCP_READ(c, b, l, r) \
        { \
            struct pollfd pfd; \
            do { \
                pfd.fd = (int)(c)->socket; \
                pfd.events = POLLIN; \
                (r) = poll(&pfd, 1, (c)->receive.timeOut * 1000); \
                if ((r) > 0) { \
                    if ((pfd.revents & (POLLIN | POLLPRI))) { \
                        do { \
                            (r) = IPrecv((c)->socket, b, l, 0);         \
                            if ((r) >= 0) {                             \
                                CONN_TRACE_DATA((c), CONN_TRACE_EVENT_READ, (b), (r)); \
                                break;                                  \
                            } else if (errno == EINTR) {                \
                                continue;                               \
                            }            \
                            CONN_TRACE_ERROR((c), "RECV", (r));         \
                            (r) = -1;                                   \
                            break;                                      \
                        } while (TRUE);                                 \
                        break; \
                    } else if ((pfd.revents & (POLLERR | POLLHUP | POLLNVAL))) { \
                        CONN_TRACE_ERROR((c), "POLL EVENT", (r));       \
                        (r) = -1;                                       \
                        break; \
                    } \
                } \
                if (errno == EINTR) { \
                    continue; \
                } \
                CONN_TRACE_ERROR((c), "POLL", (r)); \
               (r) = -1; \
                break; \
            } while (TRUE); \
        }

#define CONN_TCP_WRITE(c, b, l, r) \
        { \
            do { \
                (r) = IPsend((c)->socket, b, l, 0); \
                if ((r) >= 0) { \
                    CONN_TRACE_DATA((c), CONN_TRACE_EVENT_WRITE, (b), (r)); \
                    break; \
                } else if (errno == EINTR) { \
                    continue; \
                } \
                CONN_TRACE_ERROR((c), "POLL", (r)); \
                (r) = -1; \
                break; \
            } while (TRUE); \
        }

#elif defined(LIBC) || defined(NETWARE) || defined(WIN32)

#ifdef WIN32
typedef int socklen_t;
typedef unsigned int in_addr_t;
#define	ETIMEDOUT			WSAETIMEDOUT
#define	ECONNREFUSED			WSAECONNREFUSED
#define	ENETUNREACH			WSAENETUNREACH
#endif

#define ConnSockOpt(c, o, v, p, l) setsockopt((c)->socket, (o), (v), (p), (l))

#define	IPSOCKET		SOCKET
#define	IPInit()		{WSADATA data; WSAStartup(MAKEWORD(1,1), &data);}
												
#define	IPCleanup()				WSACleanup();
#define	IPsocket(domain, type, protocol)	socket(domain, type, protocol)
#define	IPaccept(s, addr, addrlen)		accept(s, addr, addrlen)
#define	IPlisten(sock, backlog)			listen(sock, backlog)
#define	IPbind(sock, addr, addrlen)		bind(sock, addr, addrlen)
#define	IPconnect(sock, addr, addrlen)		connect(sock, addr, addrlen)
#define	IPrecv(sock, buf, len, flags)		recv(sock, buf, len, flags)
#define	IPsend(sock, buf, len, flags)		send(sock, buf, len, flags)
#define	IPclose(sock)				closesocket(sock)
#define	IPshutdown(s, how)			shutdown(s, how)
#define	IPgetsockname(s, addr, addrlen)		getsockname(s, addr, addrlen)
#define	IPgetpeername(s, addr, addrlen)		getpeername(s, addr, addrlen)
#define	IPselect(nfds, rfds, wfds, efds, t)	select(nfds, rfds, wfds, efds, t)

#define CONN_TCP_READ(c, b, l, r) \
       { \
            fd_set rfds; \
            struct timeval timeout; \
            FD_ZERO(&rfds); \
            FD_SET((c)->socket, &rfds); \
            timeout.tv_usec = 0; \
            timeout.tv_sec = (c)->receive.timeOut; \
            (r) = select(FD_SETSIZE, &rfds, NULL, NULL, &timeout); \
            if ((r) > 0) { \
                (r) = IPrecv((c)->socket, (b), (l), 0); \
                CONN_TRACE_DATA_AND_ERROR((c), CONN_TRACE_EVENT_READ, (b), (r), "RECV"); \
            } else { \
                CONN_TRACE_ERROR((c), "SELECT", (r)); \
                (r) = -1; \
            } \
        }

#define CONN_TCP_WRITE(c, b, l, r) \
        (r) = IPsend((c)->socket, (b), (l), 0); \
        CONN_TRACE_DATA_AND_ERROR((c), CONN_TRACE_EVENT_WRITE, (b), (r), "SEND");

#else

#error Connection management library not implemented on this platform.

#endif

#define CONN_TCP_SEND(c, b, l, r) \
        { \
            if (!(c)->ssl.enable) { \
                CONN_TCP_WRITE((c), (b), (l), (r)); \
            } else { \
                CONN_SSL_WRITE((c), (b), (l), (r)); \
            } \
        }

#define CONN_TCP_RECEIVE(c, b, l, r) \
        { \
            if (!(c)->ssl.enable) { \
                CONN_TCP_READ((c), (b), (l), (r)); \
            } else { \
                CONN_SSL_READ((c), (b), (l), (r)); \
            } \
        }

#define CONN_TCP_FLUSH(c, b, e, r) \
        { \
            if ((b) < (e)) { \
                register char *curPTR = (char *)(b); \
                if (!(c)->ssl.enable) { \
                    while (curPTR < (e)) { \
                        CONN_TCP_WRITE((c), curPTR, (e) - curPTR, (r)); \
                        if ((r) > 0) { \
                            curPTR += (r); \
                            continue; \
                        } \
                        break; \
                    } \
                } else { \
                    while (curPTR < (e)) { \
                        CONN_SSL_WRITE((c), curPTR, (e) - curPTR, (r)); \
                        if ((r) > 0) { \
                            curPTR += (r); \
                            continue; \
                        } \
                        break; \
                    } \
                } \
                if (curPTR == (e)) { \
                    (r) = (e) - (b); \
                } else { \
                    (r) = -1; \
                } \
            } else { \
                (r) = 0; \
            } \
        }

#define CONN_TCP_CLOSE(c) \
        { \
            if ((c)->receive.buffer) { \
                (c)->receive.remaining = CONN_TCP_MTU; \
            } else { \
                (c)->receive.remaining = 0; \
            } \
            (c)->receive.read = (c)->receive.write = (c)->receive.buffer; \
            if ((c)->send.buffer) { \
                ConnFlush(c); \
                (c)->send.remaining = CONN_TCP_MTU; \
            } else { \
                (c)->send.remaining = 0; \
            } \
            (c)->send.read = (c)->send.write = (c)->send.buffer; \
            if ((c)->ssl.enable) { \
                gnutls_bye((c)->ssl.context, GNUTLS_SHUT_RDWR); \
                gnutls_deinit((c)->ssl.context); \
                (c)->ssl.context = NULL; \
                (c)->ssl.enable = FALSE; \
            } \
            if ((c)->socket != -1) { \
                IPshutdown((c)->socket, 2); \
                IPclose((c)->socket); \
                (c)->socket = -1; \
            } \
        }

#define CONN_SSL_NEW(c, s) \
        { \
            (c)->ssl.context = __gnutls_new(s); \
            if ((c)->ssl.context) \
                (c)->ssl.enable = TRUE; \
            } else { \
                (c)->ssl.context = NULL; \
                (c)->ssl.enable = FALSE; \
            } \
        }

#define CONN_SSL_FREE(c) \
        { \
            if ((c)->ssl.enable) { \
                gnutls_deinit((c)->ssl.context); \
                (c)->ssl.context = NULL; \
                (c)->ssl.enable = FALSE; \
            } \
        }

#define CONN_SSL_ACCEPT(c, s) \
        { \
            (c)->ssl.context = __gnutls_new(s); \
            if ((c)->ssl.context \
                    && (gnutls_handshake((c)->ssl.context) == 0)) { \
                (c)->ssl.enable = TRUE; \
            } else { \
                if ((c)->ssl.context) { \
                    gnutls_deinit((c)->ssl.context); \
                    (c)->ssl.context = NULL; \
                } \
                (c)->ssl.enable = FALSE; \
            } \
        }

#define CONN_SSL_CONNECT(c, s, r) \
        { \
            (c)->ssl.context = __gnutls_new(s); \
            if ((c)->ssl.context \
                    && (gnutls_handshake((c)->ssl.context) == 0)) { \
                (c)->ssl.enable = TRUE; \
            } else { \
                if ((c)->ssl.context) { \
                    gnutls_deinit((c)->ssl.context); \
                    (c)->ssl.context = NULL; \
                } \
                (c)->ssl.enable = FALSE; \
            } \
        }

#if !defined(_BONGO_CONN_TRACE)

#define CONN_TRACE_GET_FLAGS()                      0
#define CONN_TRACE_SET_FLAGS(f) 
#define CONN_TRACE_INIT(p, n) 
#define CONN_TRACE_SHUTDOWN() 
#define CONN_TRACE_CREATE_DESTINATION(t)            NULL
#define CONN_TRACE_FREE_DESTINATION(d)
#define CONN_TRACE_BEGIN(c, t, d) 
#define CONN_TRACE_END(c) 
#define CONN_TRACE_EVENT(c, t) 
#define CONN_TRACE_ERROR(c, e, l) 
#define CONN_TRACE_DATA(c, t, b, l) 
#define CONN_TRACE_DATA_AND_ERROR(c, t, b, l, e) 

#else

#define CONN_TRACE_GET_FLAGS()                      ConnTraceGetFlags()
#define CONN_TRACE_SET_FLAGS(f)                     ConnTraceSetFlags(f)
#define CONN_TRACE_INIT(p, n)                       ConnTraceInit(p, n)
#define CONN_TRACE_SHUTDOWN()                       ConnTraceShutdown()
#define CONN_TRACE_CREATE_DESTINATION(t)            ConnTraceCreatePersistentDestination(t)
#define CONN_TRACE_FREE_DESTINATION(d)              ConnTraceFreeDestination(d) 
#define CONN_TRACE_BEGIN(c, t, d)                   ConnTraceBegin((c), (t), (d))
#define CONN_TRACE_END(c)                           ConnTraceEnd((c))
#define CONN_TRACE_EVENT(c, t)                      ConnTraceEvent((c), (t), NULL, 0)
#define CONN_TRACE_ERROR(c, e, l)                   ConnTraceEvent((c), CONN_TRACE_EVENT_ERROR, (e), (l))
#define CONN_TRACE_DATA(c, t, b, l)                 ConnTraceEvent((c), (t), (b), (l))
#define CONN_TRACE_DATA_AND_ERROR(c, t, b, l, e)    if ((l) > 0) { \
                                                        ConnTraceEvent((c), (t), (b), (l)); \
                                                    } else { \
                                                        ConnTraceEvent((c), CONN_TRACE_EVENT_ERROR, (e), (l)); \
                                                    }

#endif

#define CONN_SSL_READ(c, b, l, r)           (r) = gnutls_record_recv((c)->ssl.context, (void *)(b), (l)); \
                                            CONN_TRACE_DATA_AND_ERROR((c), CONN_TRACE_EVENT_READ, (b), (r), "SSL_READ");

#define CONN_SSL_WRITE(c, b, l, r)          (r) = gnutls_record_send((c)->ssl.context, (void *)(b), (l)); \
                                            CONN_TRACE_DATA_AND_ERROR((c), CONN_TRACE_EVENT_WRITE, (b), (r), "SSL_WRITE");

typedef struct _ConnectionBuffer {
    char *read;
    char *write;

    char *buffer;

    size_t remaining;

    int timeOut;
} ConnectionBuffer;

typedef struct _ConnSSLConfiguration {
    unsigned long options;

    void *method;

    long mode;

    const char *cipherList;

    struct {
        long type;
        const unsigned char *file;
    } certificate;

    struct {
        long type;
        const unsigned char *file;
    } key;
} ConnSSLConfiguration;

typedef struct {
    FILE *file;
    unsigned long sequence;
    unsigned long startTime;
    unsigned long useCount;
} TraceDestination;

typedef struct _Connection {
    IPSOCKET socket;
    BOOL bSelfManage;

    struct {
        BOOL enable;

        gnutls_session_t context;
    } ssl;

    struct sockaddr_in socketAddress;

    ConnectionBuffer receive;
    ConnectionBuffer send;

    struct {
        struct _Connection *next;
        struct _Connection *previous;
    } queue;

    struct {
        struct _Connection *next;
        struct _Connection *previous;
    } allocated;

    struct {
        void *cb;
        void *data;
    } client;

    struct {
        char address[20];
        unsigned long type;
        char *typeName;
        unsigned long flags;
        TraceDestination *destination;
    } trace;
} Connection;

/* ccodes represent stream/connection health.  A value of -1 indicates an error */
typedef int CCode;

/* Address Pool Structure */
typedef struct {
    struct sockaddr_in addr;
    unsigned long weight;    
    unsigned long id;
    XplAtomic errorCount;
    time_t lastErrorTime;
} AddressPoolAddress;

#define ADDRESS_POOL_INITIALIZED_VALUE 1029045334

typedef struct {
    AddressPoolAddress *address;
    unsigned long addressCount;
    struct {
        unsigned long *elements;
        unsigned long count;
        unsigned long index;
        XplMutex mutex;
    } weightTable;
    XplRWLock lock;
    unsigned long version;
    unsigned long nextId;
    unsigned long errorThreshold;
    unsigned long errorTimeThreshold;
    unsigned long initialized;
} AddressPool;

void ConnAddressPoolStartup(AddressPool *pool, unsigned long errorThreshold, unsigned long errorTimeThreshold);
void ConnAddressPoolShutdown(AddressPool *pool);
BOOL ConnAddressPoolAddHost(AddressPool *pool, char *hostName, unsigned short hostPort, unsigned long weight);
BOOL ConnAddressPoolAddSockAddr(AddressPool *pool, struct sockaddr_in *addr, unsigned long weight);
BOOL ConnAddressPoolRemoveSockAddr(AddressPool *pool, struct sockaddr_in *addr);
BOOL ConnAddressPoolRemoveHost(AddressPool *pool, char *hostName, unsigned short hostPort);
Connection *ConnAddressPoolConnect(AddressPool *pool, unsigned long timeOut);

BOOL ConnStartup(unsigned long TimeOut, BOOL EnableSSL);
void ConnShutdown(void);

void ConnSSLContextFree(bongo_ssl_context *Context);
bongo_ssl_context *ConnSSLContextAlloc(ConnSSLConfiguration *ConfigSSL);

Connection *ConnAlloc(BOOL Buffers);
IPSOCKET ConnSocket(Connection *conn);
IPSOCKET ConnServerSocket(Connection *conn, int backlog);
IPSOCKET ConnServerSocketUnix(Connection *conn, const char *path, int backlog);

IPSOCKET ConnConnectWithTimeOut(Connection *conn, struct sockaddr *saddr, socklen_t slen, bongo_ssl_context *context, TraceDestination *destination, unsigned long timeOut);
IPSOCKET ConnConnectEx(Connection *conn, struct sockaddr *saddr, socklen_t slen, bongo_ssl_context *context, TraceDestination *destination);
IPSOCKET ConnConnect(Connection *conn, struct sockaddr *saddr, socklen_t slen, bongo_ssl_context *context);

int ConnEncrypt(Connection *conn, bongo_ssl_context *context);
BOOL ConnNegotiate(Connection *conn, bongo_ssl_context *Context);

int ConnClose(Connection *Conn, unsigned long Flags);
void ConnCloseAll(unsigned long Flags);

void ConnFree(Connection *Conn);

int ConnAccept(Connection *Server, Connection **Client);

int ConnSend(Connection *Conn, char *Buffer, unsigned int Length);
int ConnReceive(Connection *Conn, char *Buffer, unsigned int Length);

int ConnRead(Connection *Conn, char *Dest, int Length);
int ConnReadCount(Connection *Conn, char *Dest, int Count);
int ConnReadLine(Connection *Conn, char *Line, int Length);
int ConnReadAnswer(Connection *Conn, char *Line, int Length);
long ConnReadToAllocatedBuffer(Connection *c, char **buffer, unsigned long *bufferSize);
int ConnReadToFile(Connection *Conn, FILE *Dest, int Count);
int ConnReadToFileUntilEOS(Connection *Src, FILE *Dest);
int ConnReadToConn(Connection *Src, Connection *Dest, int Count);
int ConnReadToConnUntilEOS(Connection *Src, Connection *Dest);

int ConnWrite(Connection *Conn, const char *Source, int Count);
int ConnWriteF(Connection *Conn, const char *Format, ...) XPL_PRINTF(2, 3);
int ConnWriteVF(Connection *c, const char *format, va_list ap);
int ConnWriteFile(Connection *Conn, FILE *Source);
int ConnWriteFromFile(Connection *Conn, FILE *Source, int Count);
#define ConnWriteStr(conn, mesg) ConnWrite(conn, mesg, strlen(mesg))


int ConnFlush(Connection *Conn);

BOOL ConnTraceAvailable(void);

#if defined(_BONGO_CONN_TRACE)
void ConnTraceSetFlags(unsigned long flags);
unsigned long ConnTraceGetFlags(void);
void ConnTraceInit(char *path, char *name);
void ConnTraceShutdown(void);
TraceDestination *ConnTraceCreatePersistentDestination(unsigned char type);
void ConnTraceFreeDestination(TraceDestination *destination);
void ConnTraceBegin(Connection *c, unsigned long type, TraceDestination *destination);
void ConnTraceEnd(Connection *c);
void ConnTraceEvent(Connection *c, unsigned long event, char *buffer, long len);
#endif

/* fixme - to be deprecated */
#define XPLNETDB_DEFINE_CONTEXT

typedef int (*GenericReadFunc)(void *sktCtx, unsigned char *Buf, int Len, int readTimeout);
typedef int (*GenericWriteFunc)(void *sktCtx, unsigned char *Buf, int Len);

unsigned long XplGetHostIPAddress(void);
int XplPrintIPAddress(char *buffer, int bufLen, unsigned long address);
#define XplPrintHostIPAddress(buffer, bufLen) XplPrintIPAddress(buffer, bufLen, XplGetHostIPAddress());

BOOL XplIsLocalIPAddress(unsigned long Address);

int XplGetInterfaceList(void);
int XplDestroyInterfaceList(void);

int XplIPRead(void *sktCtx, unsigned char *Buf, int Len, int socketTimeout);
int XplIPReadSSL(void *sktCtx, unsigned char *Buf, int Len, int socketTimeout);
int XplIPWrite(void *sktCtx, unsigned char *Buf, int Len);
int XplIPWriteSSL(void *sktCtx, unsigned char *Buf, int Len);
int XplIPConnectWithTimeout(IPSOCKET soc, struct sockaddr *addr, long addrSize, long timeout);

#endif /* _BONGO_CONNIO_H */
