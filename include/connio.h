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

void CHOP_NEWLINE(unsigned char *s);
void SET_POINTER_TO_VALUE(unsigned char *p, unsigned char *s); // FIXME: Unused?

#if defined (UNIX) || defined(S390RH) || defined(SOLARIS)

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

#else

#error Connection management library not implemented on this platform.

#endif

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
        gnutls_certificate_credentials_t credentials;
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

#include <connio-trace.h>

void ConnTcpWrite(Connection *c, char *b, size_t l, int *r);
void ConnTcpRead(Connection *c, char *b, size_t l, int *r);
void ConnTcpFlush(Connection *c, const char *b, const char *e, int *r);
void ConnTcpClose(Connection *c);

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
