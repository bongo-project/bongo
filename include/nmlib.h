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
// Parts Copyright (C) 2007 Alex Hudson. See COPYING for details.

#ifndef _BONGO_NMAP_LIBRARY_H
#define _BONGO_NMAP_LIBRARY_H

#include <connio.h>
#include <nmap.h>
#include <bongocal.h>

/*
    NMAP Library Return Codes
*/

#define NMAP_ERROR_COMM                             -501  /* an IP error has occurred */
#define NMAP_ERROR_INVALID_RESPONSE_CODE            -502  /* the response code is not a 4 digit number followed by a - or space */
#define NMAP_ERROR_LINE_TOO_LONG                    -503  /* the line does not fit in the network buffer */
#define NMAP_ERROR_BUFFER_TOO_SHORT                 -504  /* the line in the network buffer is bigger than the destination buffer */
#define NMAP_ERROR_CALLBACK_FUNCTION_NOT_DEFINED    -505  /* the line in the network buffer is bigger than the destination buffer */
#define NMAP_ERROR_CALLBACK_FUNCTION_FAILED         -506  /* the line in the network buffer is bigger than the destination buffer */
#define NMAP_ERROR_BAD_PROPERTY                     -507  /* the line in the network buffer is bigger than the destination buffer */
#define NMAP_ERROR_PROTOCOL                         -508  /* the server response is unexpected */

/*
*/
#define NMAP_REGISTRATION_CLIENT_STACK_SIZE (128 * 1024)

/*
*/
typedef enum _RegistrationStates {
    REGISTRATION_LOADING, 
    REGISTRATION_ALLOCATING, 
    REGISTRATION_CONNECTING, 
    REGISTRATION_REGISTERING, 
    REGISTRATION_COMPLETED, 
    REGISTRATION_ABORT, 
    REGISTRATION_FAILED, 

    REGISTRATION_MAX_STATES
} RegistrationStates;

typedef BOOL (* NMAPOutOfBoundsCallback)(void *param, char *beginPtr, char *endPtr);


/* Mime stuff */

typedef struct _NmapMimeInfo {
    char type[MIME_TYPE_LEN + 1];
    char subtype[MIME_SUBTYPE_LEN + 1];
    char charset[MIME_CHARSET_LEN + 1];
    unsigned char encoding[MIME_ENCODING_LEN + 1];
    unsigned char name[MIME_NAME_LEN + 1];
    int start;
    int size;
    int headersize;  /* rfc822-specific, not used */
    int lines;       /* not used */
} NmapMimeInfo;

int NMAPParseMIMELine(char *line, NmapMimeInfo *info);


BOOL NMAPFindToplevelMimePart(Connection *conn, 
                              const char *id, 
                              const char *supertype, 
                              const char *subtype, 
                              NmapMimeInfo *info);
BOOL NMAPQueueFindToplevelMimePart(Connection *conn, 
                                   const char *id, 
                                   const char *supertype, 
                                   const char *subtype, 
                                   NmapMimeInfo *info);

char *NMAPReadMimePartUTF8(Connection *conn,
                           const char *guid,
                           NmapMimeInfo *info,
                           int maxRead);
char *NMAPQueueReadMimePartUTF8(Connection *conn,
                                const char *qID,
                                NmapMimeInfo *info,
                                int maxRead);


typedef struct {
    char *buffer;
    char *name;
    size_t nameLen;
    char *value;
    size_t valueLen;
} NmapProperty;

/*
*/
#define NMAPAlloc(b) ConnAlloc((b))
#define NMAPFree(c) ConnFree((c))

#define NMAPClose(c, f) ConnClose((c), (f))

#define NMAPRead(c, b, l) ConnRead((c), (b), (l))
#define NMAPReadCount(c, b, l) ConnReadCount((c), (b), (l))
#define NMAPReadToFile(c, f, l) ConnReadToFile((c), (f), (l))
#define NMAPReadToConn(s, d, l) ConnReadToConn((c), (d), (l))

#define NMAPWrite(c, b, l) ConnWrite((c), (b), (l))
#define NMAPWriteFromFile(c, f, l) ConnWriteFromFile((c), (f), (l))

#define NMAPSend(c, b, l) ConnSend((c), (b), (l))
#define NMAPReceive(c, b, l) ConnReceive((c), (b), (l))

/*
*/
BOOL NMAPInitialize();
void NMAPSetEncryption(bongo_ssl_context *context);
bongo_ssl_context *NMAPSSLContextAlloc(void);

int NMAPSendCommand(Connection *conn, const unsigned char *command, size_t length);
int NMAPSendCommandF(Connection *conn, const char *format, ...) XPL_PRINTF(2, 3);

int NMAPReadResponse(Connection *conn, unsigned char *response, size_t length, BOOL check);
int NMAPReadResponseLine(Connection *conn, unsigned char *response, size_t length, BOOL check);
int NMAPReadResponseAndCount(Connection *conn, unsigned long *count);

int NMAPRunCommandF(Connection *conn, char *response, size_t length, const char *format, ...) XPL_PRINTF(4, 5);


/* Configuration function for agents etc. */
BOOL
NMAPReadConfigFile(const unsigned char *file, unsigned char **output);

int NMAPReadCrLf(Connection *conn);
int NMAPReadPropertyValueLength(Connection *conn, const char *propertyName, size_t *propertyValueLen);
int NMAPReadTextPropertyResponse(Connection *conn, const char *propertyName, char *propertyBuffer, size_t propertyBufferLen);
int NMAPGetTextProperty(Connection *conn, uint64_t guid, const char *propertyName, char *propertyBuffer, size_t propertyBufferLen);
int NMAPSetTextProperty(Connection *conn, uint64_t guid, const char *propertyName, const char *propertyValue, unsigned long propertyValueLen);
int NMAPSetTextPropertyFilename(Connection *conn, const char *filename, const char *propertyName, const char *propertyValue, unsigned long propertyValueLen);
int NMAPReadDecimalPropertyResponse(Connection *conn, const char *propertyName, long *propertyValue);
int NMAPGetDecimalProperty(Connection *conn, uint64_t guid, const char *propertyName, long *propertyValue);
int NMAPSetDecimalProperty(Connection *conn, uint64_t guid, const char *propertyName, long propertyValue);
int NMAPReadHexadecimalPropertyResponse(Connection *conn, const char *propertyName, unsigned long *propertyValue);
int NMAPGetHexadecimalProperty(Connection *conn, uint64_t guid, const char *propertyName, unsigned long *propertyValue);
int NMAPSetHexadecimalProperty(Connection *conn, uint64_t guid, const char *propertyName, unsigned long propertyValue);

int NMAPCreateCollection(Connection *conn,
                         const char *collectionName,
                         uint64_t *guid);
int NMAPCreateCalendar(Connection *nmap,
                       const char *calendarName,
                       uint64_t *guid);

BongoCalObject *NMAPGetEvents(Connection *conn, const char *calendar, BongoCalTime start, BongoCalTime end);
BOOL NMAPAddEvent(Connection *conn, BongoCalObject *cal, const char *calendar, char *uid, int uidLen);

/* NMAPReadAnswer and NMAPReadAnswerLine are deprecated in favor of NMAPReadResponse and NMAPReadResponseLine */
int NMAPReadAnswer(Connection *conn, unsigned char *response, size_t length, BOOL checkForResult);
int NMAPReadAnswerLine(Connection *conn, unsigned char *response, size_t length, BOOL checkForResult);

Connection *NMAPConnect(unsigned char *address, struct sockaddr_in *addr);
Connection *NMAPConnectEx(unsigned char *address, struct sockaddr_in *addr, TraceDestination *destination);
Connection *NMAPConnectQueue(unsigned char *address, struct sockaddr_in *addr);
Connection *NMAPConnectQueueEx(unsigned char *address, struct sockaddr_in *addr, TraceDestination *destination);
BOOL NMAPEncrypt(Connection *conn, unsigned char *response, int length, BOOL force);

BOOL NMAPAuthenticateToStore(Connection *conn, unsigned char *response, int length);
BOOL NMAPAuthenticateToQueue(Connection *conn, unsigned char *response, int length);
int NMAPAuthenticateWithCookie(Connection *conn, const char *user, const char *cookie, unsigned char *buffer, int length);
BOOL NMAPAuthenticateThenUserAndStore(Connection *conn, unsigned char *user);


void NMAPQuit(Connection *conn);

RegistrationStates QueueRegister(const unsigned char *dn, unsigned long queue, unsigned short port);

#endif  /* _BONGO_NMAP_LIBRARY_H */
