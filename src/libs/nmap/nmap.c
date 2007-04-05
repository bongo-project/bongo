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

/** \file nmap.c
 */

#include <config.h>
#include <xpl.h>
#include <bongoutil.h>

#include <memmgr.h>

#include <nmlib.h>
#include <msgapi.h>
#include <nmap.h>
#include <bongostore.h>

struct {
    RegistrationStates state;

    unsigned char access[NMAP_HASH_SIZE];

    MDBHandle directoryHandle;

    bongo_ssl_context *context;

    BOOL debug;
} NMAPLibrary = {
    REGISTRATION_LOADING, 
    { '\0' }, 
    NULL, 
    NULL, 

    FALSE
};

int 
NMAPSendCommand(Connection *conn, const unsigned char *command, size_t length)
{
    int written;

    written = ConnWrite(conn, command, length);
    if (written < 0 || (size_t)written != length) {
        return -1;
    }

    if (ConnFlush(conn) < 0) {
        return -1;
    }
    
    return 0;
}

int
NMAPSendCommandF(Connection *conn, const char *format, ...)
{
    va_list ap;
    int written;

    va_start(ap, format);
    written = ConnWriteVF(conn, format, ap);
    va_end(ap);
    
    if (written < 0) {
        return written;
    }
    
    if (ConnFlush(conn) < 0) {
        return -1;
    } else {
        return written;
    }
}

__inline static unsigned char *
FindNewLineChar(unsigned char *Buffer, unsigned char *EndPtr) {
    register unsigned char *ptr = Buffer;
    register unsigned char *endPtr = EndPtr;

    do {
        if (ptr < endPtr) {
            if (*ptr != '\n') {
                ptr++;
                continue;
            }

            return(ptr);
        }

        break;
    } while(TRUE);

    return(NULL);
}

__inline static unsigned char *
FindEndOfLine(Connection *conn)
{
    int count;
    char *newLine;

    Connection *c = conn;

    do {
        if ((newLine = FindNewLineChar(c->receive.read, c->receive.write)) != NULL) {
            return(newLine);
        }

        count = c->receive.write - c->receive.read;

        if (count < CONN_TCP_MTU) {
            if (count == 0) {
                CONN_TCP_RECEIVE(c, c->receive.buffer, CONN_TCP_MTU, count);
                if (count > 0) {
                    c->receive.read = c->receive.buffer;
                    c->receive.write = c->receive.buffer + count;
                    c->receive.remaining = CONN_TCP_MTU - count;
                    c->receive.write[0] = '\0';
                    continue;
                }

                break;
            }

            /* preserve unread data in the buffer */
            memcpy(c->receive.buffer, c->receive.read, count);
            c->receive.read = c->receive.buffer;
            c->receive.write = c->receive.buffer + count;
            c->receive.remaining = CONN_TCP_MTU - count;
            CONN_TCP_RECEIVE(c, c->receive.write, c->receive.remaining, count);
            if (count > 0) {
                c->receive.write += count;
                c->receive.remaining -= count;
                c->receive.write[0] = '\0';
                continue;
            }

            break;
        }

        /* This line is longer than MTU size, NMAP is not supposed to do that on a response line */
        return(c->receive.write - 1);
    } while (TRUE);

    return(NULL);
}

__inline static int 
NMAPReadDataAfterResponseCode(Connection *conn, unsigned char *response, size_t length, BOOL RemoveCRLF)
{
    int ccode;
    int count;
    char *newLine;

    do {
        newLine = FindEndOfLine(conn);
        if (newLine) {
            if (*newLine == '\n') {
                newLine++;
                ccode = atol(conn->receive.read);
                if (ccode != 6000) {
                    if ((ccode > 999) && (ccode < 10000) && ((conn->receive.read[4] == ' ') || (conn->receive.read[4] == '-'))) {
                        if (RemoveCRLF) {
                            if (newLine[-2] == '\r') {
                                count = newLine - conn->receive.read - 7;
                            } else {
                                count = newLine - conn->receive.read - 6;
                            }
                        } else {
                            count = newLine - conn->receive.read - 5;
                        }

                        if ((unsigned long)count < length) {
                            memcpy(response, conn->receive.read + 5, count);
                            response[count] = '\0';
                            conn->receive.read = newLine;
                            return(ccode);
                        }

                        return(NMAP_ERROR_BUFFER_TOO_SHORT);
                    } 

                    return(NMAP_ERROR_INVALID_RESPONSE_CODE);
                }

                if (conn->client.cb) {
                    if (((NMAPOutOfBoundsCallback)conn->client.cb)(conn->client.data, conn->receive.read + 5, newLine)) {
                        conn->receive.read = newLine;
                        continue;
                    }

                    return(NMAP_ERROR_CALLBACK_FUNCTION_FAILED);
                }

                return(NMAP_ERROR_CALLBACK_FUNCTION_NOT_DEFINED);
            }

            return(NMAP_ERROR_LINE_TOO_LONG);
        }

        break;
    } while (TRUE);

    return(NMAP_ERROR_COMM);
}

__inline static int 
NMAPReadFullLine(Connection *conn, unsigned char *response, size_t length, BOOL RemoveCRLF)
{
    int ccode;
    int count;
    char *newLine;

    do {
        newLine = FindEndOfLine(conn);
        if (newLine) {
            if (*newLine == '\n') {
                newLine++;
                ccode = atol(conn->receive.read);
                if (ccode > -1) {
                    if (ccode != 6000) {
                        if (RemoveCRLF) {
                            if (newLine[-2] == '\r') {
                                count = newLine - conn->receive.read - 2;
                            } else {
                                count = newLine - conn->receive.read - 1;
                            }
                        } else {
                            count = newLine - conn->receive.read;
                        }

                        if ((unsigned long)count < length) {
                            memcpy(response, conn->receive.read, count);
                            response[count] = '\0';
                            conn->receive.read = newLine;
                            return(ccode);
                        }

                        return(NMAP_ERROR_BUFFER_TOO_SHORT);
                    }

                    if (conn->client.cb) {
                        if (((NMAPOutOfBoundsCallback)conn->client.cb)(conn->client.data, conn->receive.read + 5, newLine)) {
                            conn->receive.read = newLine;
                            continue;
                        }

                        return(NMAP_ERROR_CALLBACK_FUNCTION_FAILED);
                    }

                    return(NMAP_ERROR_CALLBACK_FUNCTION_NOT_DEFINED);
                }

                return(NMAP_ERROR_INVALID_RESPONSE_CODE);
            }

            return(NMAP_ERROR_LINE_TOO_LONG);
        }

        break;
    } while (TRUE);

    return(NMAP_ERROR_COMM);
}


__inline static int 
NMAPReadJustResponseCode(Connection *conn)
{
    int ccode;
    char *newLine;

    do {
        newLine = FindEndOfLine(conn);
        if (newLine) {
            if (*newLine == '\n') {
                newLine++;
                ccode = atol(conn->receive.read);
                if (ccode != 6000) {
                    if ((ccode > 999) && (ccode < 10000) && ((conn->receive.read[4] == ' ') || (conn->receive.read[4] == '-'))) {
                        conn->receive.read = newLine;
                        return(ccode);
                    } 

                    return(NMAP_ERROR_INVALID_RESPONSE_CODE);
                }

                if (conn->client.cb) {
                    if (((NMAPOutOfBoundsCallback)conn->client.cb)(conn->client.data, conn->receive.read + 5, newLine)) {
                        conn->receive.read = newLine;
                        continue;
                    }

                    return(NMAP_ERROR_CALLBACK_FUNCTION_FAILED);
                }

                return(NMAP_ERROR_CALLBACK_FUNCTION_NOT_DEFINED);
            }

            return(NMAP_ERROR_LINE_TOO_LONG);
        }

        break;
    } while (TRUE);

    return(NMAP_ERROR_COMM);
}

/** Used to read response lines from NMAP.

    Remarks
        - will block until a line can be read from the network
        - for every 6000 line encountered the callback function is called and the next line is found
        - copies the first non 6000 line excluding the \\r\\n into the response buffer
        - advances read pointer to the beginning of the next line on success

    @return 
        - positive numeric equivalent of the first characters on the line on success
        - negative number on failure

    @param conn the network connection to read the line from

    @param response is either a pointer to a buffer or NULL.
        - \<destination buffer\>
            - the response code will be returned
            - the line will be copied to the destination buffer
        - NULL
            - the response code will be returned

    @param length the length of the destination buffer

    @param check is a boolean value with the following effects:
        - TRUE 
            - the NMAP response code will be validated
            - only the data after the response code will be copied to the destination buffer
        - FALSE 
            - will not require the first characters read to be an NMAP response code
            - the entire line will be copied to the destination buffer

 */
int 
NMAPReadResponse(Connection *conn, unsigned char *response, size_t length, BOOL check)
{
    if (response) {
        if (check) {
            return(NMAPReadDataAfterResponseCode(conn, response, length, TRUE));
        } else {
            return(NMAPReadFullLine(conn, response, length, TRUE));
        }
    } else {
        return(NMAPReadJustResponseCode(conn));
    }
}


/* NMAPReadResponseLine

   Same as NMAPReadResponse except that it does not strip the \r\n 

 */
int 
NMAPReadResponseLine(Connection *conn, unsigned char *response, size_t length, BOOL check)
{
    if (response) {
        if (check) {
            return(NMAPReadDataAfterResponseCode(conn, response, length, FALSE));
        } else {
            return(NMAPReadFullLine(conn, response, length, FALSE));
        }
    } else {
        return(NMAPReadJustResponseCode(conn));
    }
}

/* NMAPReadResponseAndCount

    Remarks
        This function is designed for reading NMAP responses that take the form:
        
        <response code><' ' | '-'><count>
        
        2021 374                     (for example)

        This function:
        - will block until a line can be read from the network
        - for every 6000 line encountered the callback function is called and the next line is found
        - evaluates the first non 6000 line 
        - advances read pointer to the beginning of the next line on success

    Return Value
        - the NMAP response code on success
        - negative number on failure

    Parameters
        conn    - the network connection to read the line from

        count   - this variable will be assigned the numberic equivalent of 
                  the string following the NMAP response code.
 */
int 
NMAPReadResponseAndCount(Connection *conn, unsigned long *count)
{
    int ccode;
    char *newLine;

    do {
        newLine = FindEndOfLine(conn);
        if (newLine) {
            if (*newLine == '\n') {
                newLine++;
                ccode = atol(conn->receive.read);
                if (ccode != 6000) {
                    if ((ccode > 999) && (ccode < 10000) && ((conn->receive.read[4] == ' ') || (conn->receive.read[4] == '-'))) {
                        *count = atol(conn->receive.read + 5);
                        conn->receive.read = newLine;
                        return(ccode);
                    } 

                    return(NMAP_ERROR_INVALID_RESPONSE_CODE);
                }

                if (conn->client.cb) {
                    if (((NMAPOutOfBoundsCallback)conn->client.cb)(conn->client.data, conn->receive.read + 5, newLine)) {
                        conn->receive.read = newLine;
                        continue;
                    }

                    return(NMAP_ERROR_CALLBACK_FUNCTION_FAILED);
                }

                return(NMAP_ERROR_CALLBACK_FUNCTION_NOT_DEFINED);
            }

            return(NMAP_ERROR_LINE_TOO_LONG);
        }

        break;
    } while (TRUE);

    return(NMAP_ERROR_COMM);
}

__inline static int
ReadPropertyValueLen(Connection *conn, const char *propertyName, unsigned long *len)
{
    int ccode;
    long count;
    char *newLine;

    do {
        newLine = FindEndOfLine(conn);
        if (newLine) {
            if (*newLine == '\n') {
                newLine++;
                ccode = atol(conn->receive.read);
                if (ccode != 6000) {
                    if ((ccode > 999) && (ccode < 10000) && ((conn->receive.read[4] == ' ') || (conn->receive.read[4] == '-'))) {
                        if (ccode == 2001) {
                            char *ptr = conn->receive.read + 5;
                            size_t propertyNameLen = strlen(propertyName);

                            if (XplStrNCaseCmp(ptr, propertyName, propertyNameLen) == 0) {
                                ptr += propertyNameLen;
                                if (*ptr == ' ') {
                                    ptr++;
                                    count = atol(ptr);
                                    if (count > -1) {
                                        conn->receive.read = newLine;
                                        *len = (unsigned long)count;
                                        return(2001);
                                    }
                                }
                            }
                            return(NMAP_ERROR_BAD_PROPERTY);
                        }

                        conn->receive.read = newLine;
                        return(ccode);
                    } 
                    return(NMAP_ERROR_INVALID_RESPONSE_CODE);
                }

                if (conn->client.cb) {
                    if (((NMAPOutOfBoundsCallback)conn->client.cb)(conn->client.data, conn->receive.read + 5, newLine)) {
                        conn->receive.read = newLine;
                        continue;
                    }

                    return(NMAP_ERROR_CALLBACK_FUNCTION_FAILED);
                }

                return(NMAP_ERROR_CALLBACK_FUNCTION_NOT_DEFINED);
            }

            return(NMAP_ERROR_LINE_TOO_LONG);
        }

        break;
    } while (TRUE);

    return(NMAP_ERROR_COMM);
}

__inline static int
ReadCrLf(Connection *conn)
{
    char *newLine;

    if (conn->receive.write > (conn->receive.read + 1)) {
        if ((conn->receive.read[0] == '\r') && (conn->receive.read[1] == '\n')) {
            conn->receive.read += 2;
            return(2);
        }
        return(NMAP_ERROR_PROTOCOL);
    } 

    /* go to the wire */
    newLine = FindEndOfLine(conn);
    if (newLine) {
        if ((newLine == (conn->receive.read + 1)) && (conn->receive.read[0] == '\r')) {
            newLine++;
            conn->receive.read = newLine;
            return(2);
        }
        return(NMAP_ERROR_PROTOCOL);
    }
                                        
    return(NMAP_ERROR_COMM);
}

__inline static int
ReadPropertyValue(Connection *conn, char *property, long propertyLen)
{
    long ccode;

    property[propertyLen] = '\0';
    if (propertyLen > 0) {
        
        if (NMAPReadCount(conn, property, propertyLen) == propertyLen) {
            ;
        } else {
            return(NMAP_ERROR_COMM);
        }
    }

    /* eat the extra CR LF */
    if ((ccode = ReadCrLf(conn)) == 2) {
        return(propertyLen);
    }

    return(ccode);
}

__inline static int
ReadProperty(Connection *conn, const char *propertyName, char *propertyBuffer, size_t propertyBufferLen)
{
    int ccode;
    unsigned long len;

    if ((ccode = ReadPropertyValueLen(conn, propertyName, &len)) == 2001) {
        if (len < propertyBufferLen) {
            if (((ccode = ReadPropertyValue(conn, propertyBuffer, len)) > -1) && ((unsigned long)ccode == len)) {
                return(2001);
            }
            return(ccode);
        }
        return(NMAP_ERROR_BUFFER_TOO_SHORT);
    }

    return(ccode);
}

/* Used to read and toss CRLFs that are part of the protocol 

    Remarks
        - will block until a line can be read from the network
        - for every 6000 line encountered the callback function is called and the next line is found
        - copies the first non 6000 line excluding the \\r\\n into the response buffer
        - advances read pointer to the beginning of the next line on success

    @return 
        - positive numeric is the length of the text written to the response buffer.
        - negative number on failure

    @param conn the network connection to read the line from

 */

int
NMAPReadCrLf(Connection *conn)
{
    return(ReadCrLf(conn));
}


/** Used to read the initial response from the GETPROP and READ commands 

    Remarks
        - will block until a line can be read from the network
        - for every 6000 line encountered the callback function is called and the next line is found
        - copies the first non 6000 line excluding the \\r\\n into the response buffer
        - advances read pointer to the beginning of the next line on success

    @return 
        - positive numeric is the length of the text written to the response buffer.
        - negative number on failure

    @param conn the network connection to read the line from

    @param propertyName is the property name expected

    @param propertyValueLen the length of the value to be read

 */

int
NMAPReadPropertyValueLength(Connection *conn, const char *propertyName, size_t *propertyValueLen)
{
    return(ReadPropertyValueLen(conn, propertyName, (unsigned long *)propertyValueLen));
}

/** Used to read text properties responses from NMAP.

    Remarks
        - will block until a line can be read from the network
        - for every 6000 line encountered the callback function is called and the next line is found
        - copies the first non 6000 line excluding the \\r\\n into the response buffer
        - advances read pointer to the beginning of the next line on success

    @return 
        - positive numeric is the length of the text written to the response buffer.
        - negative number on failure

    @param conn the network connection to read the line from

    @param propertyName is the property name expected

    @param propertyBuffer is a pointer to a buffer where the property value will be copied

    @param propertyBufferLen the length of the property buffer


 */
int 
NMAPReadTextPropertyResponse(Connection *conn, const char *propertyName, char *propertyBuffer, size_t propertyBufferLen)
{
    return(ReadProperty(conn, propertyName, propertyBuffer, propertyBufferLen));
}

/** Used to read text properties responses from NMAP.

    Remarks
        - will block until a line can be read from the network
        - for every 6000 line encountered the callback function is called and the next line is found
        - copies the first non 6000 line excluding the \\r\\n into the response buffer
        - advances read pointer to the beginning of the next line on success

    @return 
        - positive numeric is the length of the text written to the response buffer.
        - negative number on failure

    @param conn the network connection to read the line from

    @param guid the object where the property will be read

    @param propertyName is the property name expected

    @param propertyBuffer is a pointer to a buffer where the property value will be copied

    @param propertyBufferLen the length of the property buffer


 */
int 
NMAPGetTextProperty(Connection *conn, uint64_t guid, const char *propertyName, char *propertyBuffer, size_t propertyBufferLen)
{
    if (NMAPSendCommandF(conn, "PROPGET %llx %s\r\n", guid, propertyName) != -1) {
        return(ReadProperty(conn, propertyName, propertyBuffer, propertyBufferLen));
    }
    return(-1);
}

/** Used to write text properties to NMAP.

    Remarks
        - Sends the PROPSET command to NMAP and processes the response
        - processes 6000 response lines encountered

    @return 
        - 1000 on success
        - other values between 1001 and 9999 indicate protocol error
        - negative value indicates a network error

    @param conn the network connection to read the line from

    @param guid the object where the property will be applied

    @param propertyName is the property name to be set

    @param propertyValue is the property value to be set

    @param propertyValueLen is the length of the property value to be set

 */
int 
NMAPSetTextProperty(Connection *conn, uint64_t guid, const char *propertyName, const char *propertyValue, unsigned long propertyValueLen)
{
    long ccode;

    if (NMAPSendCommandF(conn, "PROPSET %llx %s %lu\r\n", guid, propertyName, propertyValueLen) != -1) {
        ccode = NMAPReadResponse(conn, NULL, 0, 0);
        if (ccode == 2002) {
            ConnWrite(conn, propertyValue, propertyValueLen);
            if (ConnFlush(conn) != -1) {
                return(NMAPReadResponse(conn, NULL, 0, 0));
            }
            return(-1);
        }
        return(ccode);
    }
    return(-1);
}

int 
NMAPSetTextPropertyFilename(Connection *conn, const char *filename, const char *propertyName, const char *propertyValue, unsigned long propertyValueLen)
{
    long ccode;

    if (NMAPSendCommandF(conn, "PROPSET \"%s\" %s %lu\r\n", filename, propertyName, propertyValueLen) != -1) {
        ccode = NMAPReadResponse(conn, NULL, 0, 0);
        if (ccode == 2002) {
            ConnWrite(conn, propertyValue, propertyValueLen);
            if (ConnFlush(conn) != -1) {
                return(NMAPReadResponse(conn, NULL, 0, 0));
            }
            return(-1);
        }
        return(ccode);
    }
    return(-1);
}


#define MAX_DIGITS 25

/** Used to read decimal property responses from NMAP.

    Remarks
        - will block until a line can be read from the network
        - for every 6000 line encountered the callback function is called and the next line is found
        - copies the first non 6000 line excluding the \\r\\n into the response buffer
        - advances read pointer to the beginning of the next line on success

    @return 
        - 2001 on success
        - other values between 1000 and 9999 indicate protocol error
        - negative value indicates a network error

    @param conn the network connection to read the line from

    @param propertyName is the property name expected

    @param propertyValue is a pointer where the property value will be set

 */
int 
NMAPReadDecimalPropertyResponse(Connection *conn, const char *propertyName, long *propertyValue)
{
    long ccode;
    char propertyBuffer[MAX_DIGITS];

    ccode = ReadProperty(conn, propertyName, propertyBuffer, sizeof(propertyBuffer));
    if (ccode == 2001) {
        *propertyValue = atol(propertyBuffer);
        return(2001);
    }
    return(ccode);
}


/** Used to read decimal properties from NMAP.

    Remarks
        - will block until a line can be read from the network
        - for every 6000 line encountered the callback function is called and the next line is found
        - copies the first non 6000 line excluding the \\r\\n into the response buffer
        - advances read pointer to the beginning of the next line on success

    @return 
        - 2001 on success
        - other values between 1000 and 9999 indicate protocol error
        - negative value indicates a network error

    @param conn the network connection to read the line from

    @param guid the object where the property will be read

    @param propertyName is the property name expected

    @param propertyValue is a pointer where the property value will be set

 */
int 
NMAPGetDecimalProperty(Connection *conn, uint64_t guid, const char *propertyName, long *propertyValue)
{
    if (NMAPSendCommandF(conn, "PROPGET %llx %s\r\n", guid, propertyName) != -1) {
        return(NMAPReadDecimalPropertyResponse(conn, propertyName, propertyValue));
    }
    return(-1);
}

/** Used to read hex properties response from NMAP.

    Remarks
        - will block until a line can be read from the network
        - for every 6000 line encountered the callback function is called and the next line is found
        - copies the first non 6000 line excluding the \\r\\n into the response buffer
        - advances read pointer to the beginning of the next line on success

    @return 
        - 2001 on success
        - other values between 1000 and 9999 indicate protocol error
        - negative value indicates a network error

    @param conn the network connection to read the line from

    @param guid the object where the property will be read

    @param propertyName is the property name expected

    @param propertyValue is a pointer where the property value will be set

 */
int 
NMAPReadHexadecimalPropertyResponse(Connection *conn, const char *propertyName, unsigned long *propertyValue)
{
    long ccode;
    char propertyBuffer[MAX_DIGITS];

    ccode = ReadProperty(conn, propertyName, propertyBuffer, sizeof(propertyBuffer));
    if (ccode == 2001) {
        *propertyValue = (unsigned long)HexToUInt64(propertyBuffer, NULL);
        return(2001);
    }
    return(ccode);
}


/** Used to read hex properties from NMAP.

    Remarks
        - will block until a line can be read from the network
        - for every 6000 line encountered the callback function is called and the next line is found
        - copies the first non 6000 line excluding the \\r\\n into the response buffer
        - advances read pointer to the beginning of the next line on success

    @return 
        - 2001 on success
        - other values between 1000 and 9999 indicate protocol error
        - negative value indicates a network error

    @param conn the network connection to read the line from

    @param guid the object where the property will be read

    @param propertyName is the property name expected

    @param propertyValue is a pointer where the property value will be set

 */
int 
NMAPGetHexadecimalProperty(Connection *conn, uint64_t guid, const char *propertyName, unsigned long *propertyValue)
{
    if (NMAPSendCommandF(conn, "PROPGET %llx %s\r\n", guid, propertyName) != -1) {
        return(NMAPReadHexadecimalPropertyResponse(conn, propertyName, propertyValue));
    }

    return(-1);
}

/** Used to write decimal properties to NMAP.

    Remarks
        - Sends the PROPSET command to NMAP and processes the response
        - processes 6000 response lines encountered

    @return 
        - 1000 on success
        - other values between 1001 and 9999 indicate protocol error
        - negative value indicates a network error

    @param conn the network connection to read the line from

    @param guid the object where the property will be applied

    @param propertyName is the property name to be set

    @param propertyValue is the property value to be set

 */
int 
NMAPSetDecimalProperty(Connection *conn, uint64_t guid, const char *propertyName, long propertyValue)
{
    long ccode;
    char propertyBuffer[MAX_DIGITS];
    unsigned long len;

    len = snprintf(propertyBuffer, sizeof(propertyBuffer), "%d", (int)propertyValue); 
   
    if (NMAPSendCommandF(conn, "PROPSET %llx %s %lu\r\n", guid, propertyName, len) != -1) {
        ccode = NMAPReadResponse(conn, NULL, 0, 0);
        if (ccode == 2002) {
            ConnWrite(conn, propertyBuffer, len);
            if (ConnFlush(conn) != -1) {
                return(NMAPReadResponse(conn, NULL, 0, 0));
            }
            return(-1);
        }
        return(ccode);
    }
    return(-1);
}

/** Used to write hexadecimal properties to NMAP.

    Remarks
        - Sends the PROPSET command to NMAP and processes the response
        - processes 6000 response lines encountered

    @return 
        - 1000 on success
        - other values between 1001 and 9999 indicate protocol error
        - negative value indicates a network error

    @param conn the network connection to read the line from

    @param guid the object where the property will be applied

    @param propertyName is the property name to be set

    @param propertyValue is the property value to be set

 */
int 
NMAPSetHexadecimalProperty(Connection *conn, uint64_t guid, const char *propertyName, unsigned long propertyValue)
{
    long ccode;
    char propertyBuffer[MAX_DIGITS];
    unsigned long len;

    len = snprintf(propertyBuffer, sizeof(propertyBuffer), "%x", (unsigned int)propertyValue); 
   
    if (NMAPSendCommandF(conn, "PROPSET %llx %s %lu\r\n", guid, propertyName, len) != -1) {
        ccode = NMAPReadResponse(conn, NULL, 0, 0);
        if (ccode == 2002) {
            ConnWrite(conn, propertyBuffer, len);
            if (ConnFlush(conn) != -1) {
                return(NMAPReadResponse(conn, NULL, 0, 0));
            }
            return(-1);
        }
        return(ccode);
    }
    return(-1);
}

BongoCalObject *
NMAPGetEvents(Connection *conn, const char *calendar, BongoCalTime start, BongoCalTime end)
{
    int ccode;
    char *split[2];
    int n;
    int num;
    char *data;
    char starts[BONGO_CAL_TIME_BUFSIZE];
    char ends[BONGO_CAL_TIME_BUFSIZE];
    char buffer[CONN_BUFSIZE];
    BongoJsonNode *node;
    BongoJsonObject *calJson;
    BOOL done;

    printf("Before, hour is %d, tzid is %s\n", start.hour, BongoCalTimezoneGetTzid(start.tz));

    start = BongoCalTimezoneConvertTime(start, BongoCalTimezoneGetUtc());
    BongoCalTimeToIcal(start, starts, BONGO_CAL_TIME_BUFSIZE);

    printf("after, hour is %d\n", start.hour);

    
    end = BongoCalTimezoneConvertTime(end, BongoCalTimezoneGetUtc());
    BongoCalTimeToIcal(end, ends, BONGO_CAL_TIME_BUFSIZE);
    
    calJson = NULL;
    done = FALSE;
    
    ccode = NMAPRunCommandF(conn, buffer, CONN_BUFSIZE, "EVENTS %s%s D%s-%s Pnmap.document\r\n", 
                            calendar ? "C" : "",
                            calendar ? calendar : "",
                            starts, ends);
    do {
        switch (ccode) {
        case 2001 :
            ccode = NMAPReadAnswer(conn, buffer, CONN_BUFSIZE, TRUE);
            if (ccode != 2001) {
                continue;
            }
            
            n = BongoStringSplit(buffer, ' ', split, 2);
            if (n != 2 || strcmp(split[0], "nmap.document") != 0) {
                continue;
            }

            num = atoi(split[1]);
            data = MemMalloc(num);
            ccode = NMAPReadCount(conn, data, num);
            if (ccode != num) {
                MemFree(data);
                continue;
            }

            ccode = NMAPReadCount(conn, buffer, 2);
            if (ccode != 2) {
                MemFree(data);
                continue;
            }

            if (BongoJsonParseString(data, &node) == BONGO_JSON_OK &&
                node->type == BONGO_JSON_OBJECT) {
                if (calJson) {
                    BongoCalMerge(calJson, BongoJsonNodeAsObject(node));
                } else {
                    calJson = BongoJsonNodeAsObject(node);
                }
                BongoJsonNodeFreeSteal(node);
            }
                
            MemFree(data);
            
            break;
        case 1000 :
            done = TRUE;
            break;
        default :
            if (ccode < 0) {
                done = TRUE;
            }
        }
        
        if (!done) {
            ccode = NMAPReadAnswer(conn, buffer, CONN_BUFSIZE, TRUE);
        }
    } while (!done && ccode > 0);

    if (calJson) {
        return BongoCalObjectNew(calJson);
    } else {
        return NULL;
    }
}

int 
NMAPCreateCollection(Connection *conn,
                     const char *collectionName,
                     uint64_t *guid)
{
    int ccode;
    char buffer[CONN_BUFSIZE];
    
    ccode = NMAPRunCommandF(conn, 
                            buffer,
                            CONN_BUFSIZE, "CREATE %s\r\n", 
                            collectionName);

    if (ccode != 2002) {
        return ccode;
    }    

    ccode = NMAPReadAnswer(conn, buffer, CONN_BUFSIZE, TRUE);

    if (guid) {
        if (ccode == 1000) {
            char *p = strchr(buffer, ' ');
            if (p) {
                *p = '\0';
            }
            
            *guid = HexToUInt64(buffer, &p);
        } else if (ccode == 4226) {
            char *p = strchr(buffer, ' ');
            if (p) {
                p = strchr(p, ' ');
            }
            
            if (p) {
                *p = '\0';
            }
            
            *guid = HexToUInt64(buffer, &p);
        }
    }    

    return ccode;
}

int
NMAPCreateCalendar(Connection *conn,
                   const char *calendarName,
                   uint64_t *guid)
{
    char buffer[CONN_BUFSIZE];
    int ccode;
    
    ccode = NMAPRunCommandF(conn, 
                            buffer,
                            CONN_BUFSIZE, "WRITE /calendars %d 0 \"F%s\"\r\n", 
                            STORE_DOCTYPE_CALENDAR,
                            calendarName);

    if (ccode != 2002) {
        return ccode;
    }    

    ccode = NMAPReadAnswer(conn, buffer, CONN_BUFSIZE, TRUE);
    if (ccode == 1000) {
        char *p = strchr(buffer, ' ');
        if (p) {
            *p = '\0';
        }
        
        if (guid) {
            *guid = HexToUInt64(buffer, &p);
        }
    }

    return ccode;
}

BOOL
NMAPAddEvent(Connection *conn, BongoCalObject *cal, const char *calendar, char *uid, int uidLen)
{
    char *data;
    int len;
    int ccode;
    BOOL ret;
    char buffer[CONN_BUFSIZE];
    char *p;
    
    ret = FALSE;

    data = BongoJsonObjectToString(BongoCalObjectGetJson(cal));
    if (!data) {
        goto done;
    }

    len = strlen(data);

    ccode = NMAPRunCommandF(conn, buffer, CONN_BUFSIZE, "WRITE /events %d %d \"L/calendars/\"%s\r\n", STORE_DOCTYPE_EVENT, len, calendar);
    if (ccode != 2002) {
        goto done;
    }
    
    ConnWrite(conn, data, len);
    ConnFlush(conn);
    
    ccode = NMAPReadAnswer(conn, buffer, CONN_BUFSIZE, TRUE);
    if (ccode != 1000) {
        goto done;
    }
    p = strchr(buffer, ' ');
    if (p) {
        *p = '\0';
    }

    if (uid) {
        BongoStrNCpy(uid, buffer, uidLen);
    }

done :
    if (data) {
        MemFree(data);
    }
    
    return ret;
}

/** NMAPRunCommandF
 *
 *  Equivalent to NMAPSendCommandF() followed by NMAPReadResponse().
 *
 *  Return Value
 *     - server response code
 *     - -1 upon connection error
 */

int 
NMAPRunCommandF(Connection *conn, char *response, size_t length, const char *format, ...)
{
    int written;
    va_list ap;
    
    va_start(ap, format);
    written = ConnWriteVF(conn, format, ap);
    va_end(ap);
    
    if (written < 0) {
        return -1;
    }
    
    if (ConnFlush(conn) < 0) {
        return -1;
    }

    return NMAPReadResponse(conn, response, length, TRUE);
}

/** Read answer from NMAP connection. Optionally parses and returns the NMAP response number.
 *  \param[in] conn  The NMAP Connection struct.
 *  \param[out] response  The buffer to read the response into.
 *  \param[in] length  The maximum number of characters to read into the \c response buffer.
 *  \param[in] checkForResult  Enables or disables parsing out the response number.
 *  \return If the response number was requested and correctly parsed, then it returns
 *    that; -1 otherwise.
 */
int
NMAPReadAnswer(Connection *conn, unsigned char *response, size_t length, BOOL checkForResult)
{
    int len;
    int result = -1;
    size_t count;
    unsigned char *cur;

    len = ConnReadAnswer(conn, response, length);
    if (len > 0) {
        cur = response + 4;
        if (checkForResult && ((*cur == ' ') || (*cur == '-') || ((cur = strchr(response, ' ')) != NULL))) {
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

BOOL
NMAPReadConfigFile(const unsigned char *file, unsigned char **output)
{
    Connection *conn;
    char buffer[CONN_BUFSIZE + 1];
    CCode ccode;
    BOOL retcode = FALSE;

    conn = NMAPConnect("127.0.0.1", NULL);
    if (!conn) {
        printf("could not connect to store\n");
        return FALSE;
    }
    if (!NMAPAuthenticate(conn, buffer, sizeof(buffer))) {
        printf("could not authenticate to the store\n");
        goto nmapfinish;
    }

    NMAPSendCommandF(conn, "STORE _system\r\n");
    ccode = NMAPReadAnswer(conn, buffer, sizeof(buffer), TRUE);
    if (ccode != 1000) {
        printf("cannot access _system collection\n");
        goto nmapfinish;
    }

    if (-1 != NMAPSendCommandF(conn, "READ /config/%s\r\n", file)) {
        size_t count, written;

        ccode = NMAPReadPropertyValueLength(conn, "nmap.document", &count);
        if (ccode != 2001) {
             printf("couldn't load config from store\n");
             goto nmapfinish;
        }
		
        *output = malloc(sizeof(unsigned char) * (count+1));
        written = NMAPReadCount(conn, *output, count);
        NMAPReadCrLf(conn);
        if (written != count) {
            printf("couldn't read config from store\n");
            goto nmapfinish;
        }
    }
    retcode = TRUE;

nmapfinish:
    NMAPQuit(conn);
    ConnFree(conn);
    return retcode;
}
                                                                                                                                                                            
int
NMAPReadAnswerLine(Connection *conn, unsigned char *response, size_t length, BOOL checkForResult)
{
    int len;
    int result = -1 ; 
    size_t count;
    unsigned char *cur;

    len = ConnReadLine(conn, response, length);
    if (len > 0) {
        cur = response + 4;
        if (checkForResult && ((*cur == ' ') || (*cur == '-') || ((cur = strchr(response, ' ')) != NULL))) {
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

__inline static Connection *
NmapConnect(unsigned char *address, struct sockaddr_in *addr, int port, TraceDestination *destination)
{
    int ccode;
    Connection *conn;

    if (address || addr) {
        conn = ConnAlloc(TRUE);
    } else {
        return(NULL);
    }

    if (conn) {
        memset(&conn->socketAddress, 0, sizeof(struct sockaddr_in));

        if (address) {
            conn->socketAddress.sin_family = AF_INET;
            conn->socketAddress.sin_addr.s_addr = inet_addr(address);
            conn->socketAddress.sin_port = htons(port);
        } else {
            conn->socketAddress.sin_family = addr->sin_family;
            conn->socketAddress.sin_addr.s_addr = addr->sin_addr.s_addr;
            conn->socketAddress.sin_port = addr->sin_port;
        }

        conn->socket = IPsocket(PF_INET, SOCK_STREAM, IPPROTO_TCP);
        if (conn->socket != -1) {
            ccode = IPconnect(conn->socket, (struct sockaddr *)&(conn->socketAddress), sizeof(struct sockaddr_in));
            CONN_TRACE_BEGIN(conn, CONN_TYPE_NMAP, destination);
            CONN_TRACE_EVENT(conn, CONN_TRACE_EVENT_CONNECT);
            if (!ccode) {
                /* Set TCP non blocking io */
                ccode = 1;
                setsockopt(conn->socket, IPPROTO_TCP, 1, (unsigned char *)&ccode, sizeof(ccode));

                return(conn);
            }

            CONN_TRACE_ERROR(conn, "connect", ccode);
            CONN_TRACE_EVENT(conn, CONN_TRACE_EVENT_CLOSE);
            CONN_TRACE_END(conn);
            IPclose(conn->socket);
            conn->socket = -1;
        }

        ConnFree(conn);
    }

    return(NULL);
}

Connection *
NMAPConnect(unsigned char *address, struct sockaddr_in *addr)
{
    return(NmapConnect(address, addr, NMAP_PORT, NULL));
}

Connection *
NMAPConnectEx(unsigned char *address, struct sockaddr_in *addr, TraceDestination *destination)
{
    return(NmapConnect(address, addr, NMAP_PORT, destination));
}

Connection *
NMAPConnectQueue(unsigned char *address, struct sockaddr_in *addr)
{
    return(NmapConnect(address, addr, BONGO_QUEUE_PORT, NULL));    
}

Connection *
NMAPConnectQueueEx(unsigned char *address, struct sockaddr_in *addr, TraceDestination *destination)
{
    return(NmapConnect(address, addr, BONGO_QUEUE_PORT, NULL));    
}
                                                                          
BOOL 
NMAPEncrypt(Connection *conn, unsigned char *response, int length, BOOL force)
{
    int ccode;
    BOOL result;
    Connection *c = conn;

    result = c->ssl.enable = FALSE;
    if (!force && XplIsLocalIPAddress(conn->socketAddress.sin_addr.s_addr)) {
        return(TRUE);
    }

    if (NMAPLibrary.context) {
        setsockopt(c->socket, IPPROTO_TCP, 1, (unsigned char *)&ccode, sizeof(ccode));

        if (((ccode = ConnWrite(c, "CAPA\r\n", 6)) != -1) 
                && ((ccode = ConnFlush(c)) != -1)) {
            while ((ccode = NMAPReadAnswer(c, response, length, TRUE)) == 2001) {
                if (strcmp(response, "TLS Encryption Extension") != 0) {
                    continue;
                }

                result = TRUE;
            }

            if (ccode != 1000) {
                result = FALSE;
            }
        }

        if (result) {
                ConnWrite(c, "TLS\r\n", 5);
                ConnFlush(c);
                if ((NMAPReadAnswer(c, response, length, TRUE)) == 1000) {
                    c->ssl.enable = TRUE;
                    NMAPLibrary.context = NMAPSSLContextAlloc();
                    if (NMAPLibrary.context) {
                        if (ConnNegotiate(c, NMAPLibrary.context)) {
                            CONN_TRACE_EVENT(c, CONN_TRACE_EVENT_SSL_CONNECT);
                            return(TRUE);
                        }
                        ConnSSLContextFree(NMAPLibrary.context);
                    }
                    NMAPLibrary.context = NULL;
                }
        }
    }

    return(FALSE);
}

int
NMAPAuthenticateWithCookie(Connection *conn, const char *user, const char *cookie,
                           unsigned char *buffer, int length)
{
    int ccode;

    ccode = NMAPReadAnswer(conn, buffer, length, TRUE);
    switch(ccode) {
    case 1000:
    case 4242:
        return NMAPRunCommandF(conn, buffer, length, "AUTH USER %s %s NOSTORE\r\n",
                                user, cookie);
    default:
        return ccode;
    }
}


BOOL 
NMAPAuthenticateToStore(Connection *conn, unsigned char *response, int length)
{
    int ccode;

    ccode = NMAPReadAnswer(conn, response, length, TRUE);
    switch (ccode) {
        case 1000: {
            NMAPEncrypt(conn, response, length, FALSE);
            return(TRUE);
        }

        case 4242: {
            unsigned char    	*ptr;
            unsigned char    	*salt;
            unsigned char    	message[XPLHASH_MD5_LENGTH];
            xpl_hash_context	ctx;

            ptr = strchr(response, '<');
            if (ptr) {
                salt = ++ptr;

                if ((ptr = strchr(ptr, '>')) != NULL) {
                    *ptr = '\0';
                }

                XplHashNew(&ctx, XPLHASH_MD5);
                XplHashWrite(&ctx, salt, strlen(salt));
                XplHashWrite(&ctx, NMAPLibrary.access, NMAP_HASH_SIZE);
                XplHashFinal(&ctx, XPLHASH_LOWERCASE, message, XPLHASH_MD5_LENGTH);

                NMAPEncrypt(conn, response, length, FALSE);

                ConnWrite(conn, "AUTH SYSTEM ", 12);
                ConnWrite(conn, message, 32);
                ConnWrite(conn, "\r\n", 2);

                if ((ccode = ConnFlush(conn)) == 46) {
                    if ((ccode = NMAPReadAnswer(conn, response, length, TRUE)) == 1000) {
                        return(TRUE);
                    }
                }
            }

            /*
                Fall through to the default case statement.
            */
        }

        default: {
            break;
        }
    }

    return(FALSE);
}


BOOL 
NMAPAuthenticate(Connection *conn, unsigned char *response, int length)
{
    int ccode;

    ccode = NMAPReadAnswer(conn, response, length, TRUE);
    switch (ccode) {
        case 1000: {
            NMAPEncrypt(conn, response, length, FALSE);
            return(TRUE);
        }

        case 4242: {
            unsigned char    	*ptr;
            unsigned char    	*salt;
            unsigned char    	message[XPLHASH_MD5_LENGTH];
            xpl_hash_context	ctx;

            ptr = strchr(response, '<');
            if (ptr) {
                salt = ++ptr;

                if ((ptr = strchr(ptr, '>')) != NULL) {
                    *ptr = '\0';
                }

                XplHashNew(&ctx, XPLHASH_MD5);
                XplHashWrite(&ctx, salt, strlen(salt));
                XplHashWrite(&ctx, NMAPLibrary.access, NMAP_HASH_SIZE);
                XplHashFinal(&ctx, XPLHASH_LOWERCASE, message, XPLHASH_MD5_LENGTH);

                NMAPEncrypt(conn, response, length, FALSE);

                ConnWrite(conn, "AUTH SYSTEM ", 12);
                ConnWrite(conn, message, 32);
                ConnWrite(conn, "\r\n", 2);

                if ((ccode = ConnFlush(conn)) == 46) {
                    if ((ccode = NMAPReadAnswer(conn, response, length, TRUE)) == 1000) {
                        return(TRUE);
                    }
                }
            }

            /*
                Fall through to the default case statement.
            */
        }

        default: {
            break;
        }
    }

    return(FALSE);
}

void 
NMAPQuit(Connection *conn)
{
    ConnWrite(conn, "QUIT\r\n", 6);

    CONN_TCP_CLOSE(conn);

    return;
}

__inline static RegistrationStates
RegisterWithQueueServer(char *queueServerIpAddress, unsigned short queueServerPort, unsigned long queueNumber, const char *queueAgentServerDn, const char *queueAgentCn, unsigned long queueAgentPort)
{
    unsigned long j;
    Connection *conn = NULL;
    unsigned char response[CONN_BUFSIZE + 1];

    NMAPLibrary.state = REGISTRATION_CONNECTING;

    do {
        conn = NmapConnect(queueServerIpAddress, NULL, queueServerPort, NULL);
        if (conn) {
            NMAPLibrary.state = REGISTRATION_REGISTERING;
            break;
        }

        for (j = 0; (j < 15) && (NMAPLibrary.state == REGISTRATION_CONNECTING); j++) {
            XplDelay(1000);
        }
    } while (NMAPLibrary.state == REGISTRATION_CONNECTING);

    if (NMAPLibrary.state == REGISTRATION_REGISTERING) {
        if (NMAPAuthenticate(conn, response, CONN_BUFSIZE)) {
            if (ConnWriteF(conn, "QWAIT %lu %d %s%s%lu\r\n", queueNumber, ntohs(queueAgentPort), queueAgentServerDn, queueAgentCn, queueNumber) > 0) {
                if (ConnFlush(conn) > -1) {
                    if (NMAPReadAnswer(conn, response, CONN_BUFSIZE, TRUE) == 1000) {
                        NMAPLibrary.state = REGISTRATION_COMPLETED;
                    }
                } 
            }
        }

        NMAPQuit(conn);
    }

    if (conn) {
        ConnFree(conn);
    }
 
    return(NMAPLibrary.state);
}

RegistrationStates 
NMAPRegister(const unsigned char *queueAgentCn, unsigned long queueNumber, unsigned short queueAgentPort)
{
    unsigned long i;
    MDBValueStruct *queueServerDns = NULL;
    MDBValueStruct *details = NULL;
    unsigned short queueServerPort;
    char *ptr;

    if (!queueAgentCn) {
        NMAPLibrary.state = REGISTRATION_FAILED;
        return(NMAPLibrary.state);
    }

    NMAPLibrary.state = REGISTRATION_ALLOCATING;

    queueServerDns = MDBCreateValueStruct(NMAPLibrary.directoryHandle, MsgGetServerDN(NULL));
    if (queueServerDns == NULL) {
        NMAPLibrary.state = REGISTRATION_FAILED;
        return(NMAPLibrary.state);
    }

    MDBReadDN(queueAgentCn, MSGSRV_A_MONITORED_QUEUE, queueServerDns);
    if ((queueServerDns->Used == 0) || (details = MDBCreateValueStruct(NMAPLibrary.directoryHandle, NULL)) == NULL) {
        /* Failed to find a configuration object, use default connection information */
        MDBDestroyValueStruct(queueServerDns);
        XplConsolePrintf("Couldn't find configuration object for %s, attempting to connect to NMAP on 127.0.0.1\n", queueAgentCn);
        RegisterWithQueueServer("127.0.0.1", BONGO_QUEUE_PORT, queueNumber, MsgGetServerDN(NULL), queueAgentCn, queueAgentPort);
        if (NMAPLibrary.state != REGISTRATION_COMPLETED) {
            NMAPLibrary.state = REGISTRATION_FAILED;
        }
        return(NMAPLibrary.state);
    }

    NMAPLibrary.state = REGISTRATION_CONNECTING;

    for (i = 0; (i < queueServerDns->Used) && (NMAPLibrary.state == REGISTRATION_CONNECTING); i++) {
        /* check for a non-standard port */
        MDBRead(queueServerDns->Value[i], MSGSRV_A_PORT, details);
        if (details->Used == 0) {
            queueServerPort = BONGO_QUEUE_PORT;
        } else {
            queueServerPort = (unsigned short)atol(details->Value[0]);
            MDBFreeValues(details);
        }

        /* find the ip address on the host server object */
        if ((ptr = strrchr(queueServerDns->Value[i], '\\')) != NULL) {
            *ptr = '\0';
            MDBRead(queueServerDns->Value[i], MSGSRV_A_IP_ADDRESS, details);
            *ptr = '\\';
        } else {
            MDBRead(queueServerDns->Value[i], MSGSRV_A_IP_ADDRESS, details);
        }

        if (details->Used > 0) {
            RegisterWithQueueServer(details->Value[0], queueServerPort, queueNumber, MsgGetServerDN(NULL), queueAgentCn, queueAgentPort);
            MDBFreeValues(details);
        } else {
            RegisterWithQueueServer("127.0.0.1", queueServerPort, queueNumber, MsgGetServerDN(NULL), queueAgentCn, queueAgentPort);
        }
    }

    if (NMAPLibrary.state != REGISTRATION_COMPLETED) {
        NMAPLibrary.state = REGISTRATION_FAILED;
    }

    MDBDestroyValueStruct(queueServerDns);
    MDBDestroyValueStruct(details);
    return(NMAPLibrary.state);
}

void 
NMAPSetEncryption(bongo_ssl_context *context)
{
    NMAPLibrary.context = context;

    return;
}

bongo_ssl_context *
NMAPSSLContextAlloc(void)
{
    ConnSSLConfiguration config;
    
    config.certificate.file = MsgGetTLSCertPath(NULL);
    config.key.type = GNUTLS_X509_FMT_PEM;
    config.key.file = MsgGetTLSKeyPath(NULL);

    return ConnSSLContextAlloc(&config);
}

BOOL 
NMAPInitialize(MDBHandle directoryHandle)
{
    BOOL result = FALSE;
    MDBValueStruct *v;

    if (directoryHandle) {
        NMAPLibrary.directoryHandle = directoryHandle;

        v = MDBCreateValueStruct(NMAPLibrary.directoryHandle, NULL);
        if (v) {
            MDBRead(MSGSRV_ROOT, MSGSRV_A_ACL, v);
            if (v->Used) {
                result = HashCredential(MsgGetServerDN(NULL), v->Value[0], NMAPLibrary.access);
            }

            MDBDestroyValueStruct(v);

        }
    }

    return(result);
}
