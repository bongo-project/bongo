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

#include <logger.h>

#include <mdb.h>
#include <msgapi.h>

/* Management Client Header Include */
#include <management.h>

#include <cmlib.h>
#include "imapd.h"


static long
UserAuthenticate(ImapSession *session, unsigned char **userName, unsigned char *password, struct sockaddr_in *storeAddress)
{
    MDBValueStruct *user;
    char *tmpName;

    user = MDBCreateValueStruct(Imap.directory.handle, NULL);
    if (user) {
        if (MsgFindObject(*userName, session->user.dn, NULL, storeAddress, user)) {
            if (MsgGetUserFeature(session->user.dn, FEATURE_IMAP, NULL, NULL)) {
                if (!password || MDBVerifyPassword(session->user.dn, password, user)) {
                    
                    if (strcmp(*userName, user->Value[0]) == 0) {
                        MDBDestroyValueStruct(user);
                        return(STATUS_CONTINUE);
                    }

                    tmpName = MemStrdup(user->Value[0]);
                    MDBDestroyValueStruct(user);
                    if (tmpName) {
                        MemFree(*userName);
                        *userName = tmpName;
                        return(STATUS_CONTINUE);
                    }
                    return(STATUS_MEMORY_ERROR);
                }

                LoggerEvent(Imap.logHandle, LOGGER_SUBSYSTEM_AUTH, LOGGER_EVENT_WRONG_PASSWORD, LOG_NOTICE, 
                            0, *userName, password, XplHostToLittle(session->client.conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
                MDBDestroyValueStruct(user);
                return(STATUS_WRONG_PASSWORD);
            }
                
            LoggerEvent(Imap.logHandle, LOGGER_SUBSYSTEM_AUTH, LOGGER_EVENT_DISABLED_FEATURE, LOG_NOTICE, 
                        0, *userName, NULL, XplHostToLittle(session->client.conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
            MDBDestroyValueStruct(user);
            return(STATUS_FEATURE_DISABLED);
        }

        LoggerEvent(Imap.logHandle, LOGGER_SUBSYSTEM_AUTH, LOGGER_EVENT_UNKNOWN_USER, LOG_NOTICE, 
                    0, *userName, password, XplHostToLittle(session->client.conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
        MDBDestroyValueStruct(user);
        return(STATUS_USERNAME_NOT_FOUND);
    }
    return(STATUS_IDENTITY_STORE_FAILURE);
}

__inline static long
UserStoreConnect(ImapSession *session, struct sockaddr_in *store)
{
    unsigned char buffer[CONN_BUFSIZE + 1];

    if ((session->store.conn = NMAPConnectEx(NULL, store, session->client.conn->trace.destination)) != NULL) {
        if (NMAPAuthenticateToStore(session->store.conn, buffer, sizeof(buffer))) {
            /* Set up NMAP event handler */
            session->store.conn->client.cb = (void *)EventsCallback;
            session->store.conn->client.data = (void *)session;
            return(STATUS_CONTINUE);
        }

        LoggerEvent(Imap.logHandle, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NMAP_UNAVAILABLE, LOG_ERROR, 0, NULL, NULL, store->sin_addr.s_addr, 0, NULL, 0);                
        NMAPQuit(session->store.conn);
        session->store.conn = NULL;
        return(STATUS_NMAP_AUTH_ERROR);
    }

    LoggerEvent(Imap.logHandle, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NMAP_UNAVAILABLE, LOG_ERROR, 0, NULL, NULL, store->sin_addr.s_addr, 0, NULL, 0);                
    return(STATUS_NMAP_CONNECT_ERROR);
}

__inline static long
UserStoreInitialize(ImapSession *session, unsigned char *username)
{
    long ccode;

    if (NMAPSendCommandF(session->store.conn, "USER %s\r\n", username) != -1) {
        ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
        if (ccode == 1000) {
            if (NMAPSendCommandF(session->store.conn, "STORE %s\r\n", username) != -1) {
                ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
                if (ccode == 1000) {
                    ccode = FolderListInitialize(session);
                    if (ccode == STATUS_CONTINUE) {
                        return(STATUS_CONTINUE);
                    }
                } else {
                    ccode = CheckForNMAPCommError(ccode);
                }
            } else {
                ccode = STATUS_NMAP_COMM_ERROR;
            }
        } else {
            ccode = CheckForNMAPCommError(ccode);
        }
    } else {
        ccode = STATUS_NMAP_COMM_ERROR;
    }

    LoggerEvent(Imap.logHandle, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NMAP_UNAVAILABLE, LOG_ERROR, 
                0, NULL, NULL, session->store.conn->socketAddress.sin_addr.s_addr, ccode, NULL, 0);
    return(ccode);
}

__inline static long
UserLogin(ImapSession *session, unsigned char **username, unsigned char *password)
{
    long ccode;
    struct sockaddr_in store;

    ccode = UserAuthenticate(session, username, password, &store);
    if (ccode == STATUS_CONTINUE) {
        ccode = UserStoreConnect(session, &store);
        if (ccode == STATUS_CONTINUE) {
            ccode = UserStoreInitialize(session, *username);
            if (ccode == STATUS_CONTINUE) {
                LoggerEvent(Imap.logHandle, LOGGER_SUBSYSTEM_AUTH, LOGGER_EVENT_LOGIN, LOG_INFO, 
                            0, *username, NULL, XplHostToLittle(session->client.conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
                return(STATUS_CONTINUE);
            }
        }
    }

    return(ccode);
}

/* in X500 spaces and underscores are treated the same in distinguished names */
__inline static void
X500Encode(unsigned char *in)
{
    unsigned char *spacePtr;

    do {
        spacePtr = strchr(in, ' ');
        if (!spacePtr) {
            return;
        }
        *spacePtr = '_';
    } while (TRUE);
}



__inline static long
SendLoginFailure(ImapSession *session, unsigned char *username, unsigned char *command, long result)
{
    if ((result == STATUS_WRONG_PASSWORD) || (result == STATUS_USERNAME_NOT_FOUND)) {
        XplDelay(2000);
        session->client.authFailures++;
        if (session->client.authFailures > MAX_FAILED_LOGINS) {
            LoggerEvent(Imap.logHandle, LOGGER_SUBSYSTEM_AUTH, LOGGER_EVENT_MAX_FAILED_LOGINS, LOG_NOTICE, 
                        0, username, NULL, XplHostToLittle(session->client.conn->socketAddress.sin_addr.s_addr), MAX_FAILED_LOGINS, NULL, 0);
            if (ConnWrite(session->client.conn, "* BYE IMAP4rev1 Server signing off\r\n", sizeof("* BYE IMAP4rev1 Server signing off\r\n") - 1) != -1) {
                if (SendError(session->client.conn, session->command.tag, "LOGIN", STATUS_PERMISSION_DENIED) == STATUS_CONTINUE) {
                    ConnFlush(session->client.conn);
                }
            }
            return(STATUS_ABORT);
        }
        result = STATUS_PERMISSION_DENIED;
    }
    return(SendError(session->client.conn, session->command.tag, command, result));
}


__inline static long
GetLoginCredentials(ImapSession *session, unsigned char **username, unsigned char **password)
{
    long ccode;

    if ((ccode = ConnWrite(session->client.conn, "+ VXNlcm5hbWU6AA==\r\n", 20) != -1) && (ccode = ConnFlush(session->client.conn)) != -1) {
        ccode = ReadCommandLine(session->client.conn, &(session->command.buffer), &(session->command.bufferLen));
        if (ccode == STATUS_CONTINUE) {
            *username = MemStrdup(session->command.buffer);
            if (*username) {
                DecodeBase64(*username);
                if ((ConnWrite(session->client.conn, "+ UGFzc3dvcmQ6AA==\r\n", 20) != -1) && (ConnFlush(session->client.conn) != -1)) {
                    ccode = ReadCommandLine(session->client.conn, &(session->command.buffer), &(session->command.bufferLen));
                    if (ccode == STATUS_CONTINUE) {
                        *password = MemStrdup(session->command.buffer);
                        if (*password) {
                            DecodeBase64(*password);
                            return(STATUS_CONTINUE);
                        }
                        ccode = STATUS_MEMORY_ERROR;
                    }
                    MemFree(*username);
                    return(ccode);
                }
                MemFree(*username);
                return(STATUS_ABORT);
            }
            ccode = STATUS_MEMORY_ERROR;
        }
        return(SendError(session->client.conn, session->command.tag, "AUTHENTICATE", ccode));
    }
    return(STATUS_ABORT);
}

/********** IMAP session commands - not authenticated state (STARTTLS, AUTHENTICATE, LOGIN) **********/

int
ImapCommandAuthenticate(void *param)
{
    ImapSession *session = (ImapSession *)param;
    long ccode;
    unsigned char *username;
    unsigned char *password;

    if (session->client.state == STATE_FRESH) {
        if (XplStrNCaseCmp(session->command.buffer + 13, "LOGIN", 5) == 0) {
            ccode = GetLoginCredentials(session, &username, &password);
            if (ccode == STATUS_CONTINUE) {
                X500Encode(username);
                ccode = UserLogin(session, &username, password);
                MemFree(password);
                if (ccode == STATUS_CONTINUE) {
                    session->user.name = username;
                    session->client.state = STATE_AUTH;
                    ccode = SendOk(session,"AUTHENTICATE");
                    if (ccode == STATUS_CONTINUE) {
                        /* We are flushing here so that we can update the connection manager */
                        /* while the client is working on the response and sending the next command */
                        if (ConnFlush(session->client.conn) != -1) {
// Reenable me              CMAuthenticated((unsigned long)session->client.conn->socketAddress.sin_addr.s_addr, session->user.name);
                            return(STATUS_CONTINUE);
                        }
                    }
                }
                ccode = SendLoginFailure(session, username, "AUTHENTICATE", ccode); 
                MemFree(username);
                return(ccode);
            }
            return(SendError(session->client.conn, session->command.tag, "AUTHENTICATE", ccode));
        }
        return(SendError(session->client.conn, session->command.tag, "AUTHENTICATE", STATUS_UNKNOWN_AUTH));
    }
    return(SendError(session->client.conn, session->command.tag, "AUTHENTICATE", STATUS_INVALID_STATE));
}

int
ImapCommandLogin(void *param)
{
    ImapSession *session = (ImapSession *)param;
    long ccode;
    unsigned char *username = NULL;
    unsigned char *password = NULL;

    if (session->client.state == STATE_FRESH) {
        ccode = GrabTwoArguments(session, session->command.buffer + 6, &username, &password);
        if (ccode == STATUS_CONTINUE) {
            X500Encode(username);
            ccode = UserLogin(session, &username, password);
            MemFree(password);
            if (ccode == STATUS_CONTINUE) {
                session->user.name = username;
                session->client.state = STATE_AUTH;
                ccode = SendOk(session,"LOGIN");
                if (ccode == STATUS_CONTINUE) {
                    /* We are flushing here so that we can update the connection manager */
                    /* while the client is working on the response and sending the next command */
                    if (ConnFlush(session->client.conn) != -1) {
// Reenable me                        CMAuthenticated((unsigned long)session->client.conn->socketAddress.sin_addr.s_addr, session->user.name);
                        return(STATUS_CONTINUE);
                    } else {
                        ccode = STATUS_ABORT;
                    }
                }
            }
            ccode = SendLoginFailure(session, username, "LOGIN", ccode); 
            MemFree(username);
            return(ccode);
        }
        return(SendError(session->client.conn, session->command.tag, "LOGIN", ccode));
    }
    return(SendError(session->client.conn, session->command.tag, "LOGIN", STATUS_INVALID_STATE));
}

int
ImapCommandStartTls(void *param)
{
    long ccode;

    ImapSession *session = (ImapSession *)param;

    if (session->client.state < STATE_AUTH) {
        if (Imap.server.ssl.config.options) {
            if (((ccode = ConnWriteF(session->client.conn, "%s OK Begin TLS negotiation\r\n",session->command.tag)) != -1) && (ccode = ConnFlush(session->client.conn))) {
                session->client.conn->ssl.enable = TRUE;    
                if (ConnNegotiate(session->client.conn, Imap.server.ssl.context)) {
                    return(STATUS_CONTINUE);
                }

                return(SendError(session->client.conn, session->command.tag, "STARTTLS", STATUS_TLS_NEGOTIATION_FAILURE));
            } 

            return(STATUS_ABORT);
        }

        return(SendError(session->client.conn, session->command.tag, "", STATUS_UNKNOWN_COMMAND));
    }

    return(SendError(session->client.conn, session->command.tag, "STARTTLS", STATUS_INVALID_STATE));


}

#if MDB_DEBUG
/********** Mdb test commands - any state (User Lookup, User Verify, UserRead) **********/
int
ImapCommandUserLookup(void *param)
{
    MDBValueStruct *user;
    ImapSession *session = (ImapSession *)param;
    char *username;
    struct sockaddr_in storeAddress;

    if ((user = MDBCreateValueStruct(Imap.directory.handle, NULL)) != NULL) {
        username = session->command.buffer + strlen("USER LOOKUP");
        if (*username == ' ') {
            username++;
            if (MsgFindObject(username, session->user.dn, NULL, &storeAddress, user)) {
                if (ConnWriteF(session->client.conn, "* %s\r\n", session->user.dn) == -1) {
                    MDBDestroyValueStruct(user);
                    return(STATUS_ABORT);
                }
            }
            MDBDestroyValueStruct(user);
            return(SendOk(session, "USER LOOKUP"));
        }
        MDBDestroyValueStruct(user);
        return(SendError(session->client.conn, session->command.tag, "USER LOOKUP", STATUS_INVALID_ARGUMENT));
    }
    return(SendError(session->client.conn, session->command.tag, "USER LOOKUP", STATUS_IDENTITY_STORE_FAILURE));
}

int
ImapCommandUserVerify(void *param)
{
    MDBValueStruct *user;
    ImapSession *session = (ImapSession *)param;
    unsigned char *userDn;
    unsigned char *password;
    char *spacePtr;

    userDn = session->command.buffer + strlen("USER VERIFY");
    if (*userDn == ' ') {
        userDn++;
        spacePtr = strchr(userDn, ' ');
        if (spacePtr) {
            password = spacePtr + 1;
            if ((user = MDBCreateValueStruct(Imap.directory.handle, NULL)) != NULL) {
                *spacePtr = '\0';
                if (MDBVerifyPassword(userDn, password, user)) {
                    *spacePtr = ' ';
                    MDBDestroyValueStruct(user);
                    return(SendOk(session, "USER VERIFY"));
                }
                *spacePtr = ' ';
                MDBDestroyValueStruct(user);
                return(SendError(session->client.conn, session->command.tag, "USER VERIFY", STATUS_USERNAME_NOT_FOUND));
            }
            return(SendError(session->client.conn, session->command.tag, "USER VERIFY", STATUS_IDENTITY_STORE_FAILURE));
        }
    }
    return(SendError(session->client.conn, session->command.tag, "USER VERIFY", STATUS_INVALID_ARGUMENT));
}

int
ImapCommandUserRead(void *param)
{
    MDBValueStruct *user;
    ImapSession *session = (ImapSession *)param;
    unsigned char *userDn;
    unsigned char *attribute;
    char *spacePtr;
    unsigned long i;

    userDn = session->command.buffer + strlen("USER READ");
    if (*userDn == ' ') {
        userDn++;
        spacePtr = strchr(userDn, ' ');
        if (spacePtr) {
            attribute = spacePtr + 1;
            if ((user = MDBCreateValueStruct(Imap.directory.handle, NULL)) != NULL) {
                *spacePtr = '\0';
                MsgGetUserSetting(userDn, attribute, user);
                for(i = 0; i < user->Used; i++) {
                    if (ConnWriteF(session->client.conn, "* %s\r\n", user->Value[i]) == -1) {
                        *spacePtr = ' ';
                        MDBDestroyValueStruct(user);
                        return(STATUS_ABORT);
                    }
                }
                *spacePtr = ' ';
                MDBDestroyValueStruct(user);
                return(SendOk(session, "USER READ"));
            }
            return(SendError(session->client.conn, session->command.tag, "USER READ", STATUS_IDENTITY_STORE_FAILURE));
        }
    }
    return(SendError(session->client.conn, session->command.tag, "USER READ", STATUS_INVALID_ARGUMENT));
}

int
ImapCommandUserWriteLocal(void *param)
{
    long ccode;
    ImapSession *session = (ImapSession *)param;
    unsigned char *userDn;
    unsigned char *attribute;
    unsigned long count;
    unsigned long i;
    unsigned char *ptr;
    
    ptr = session->command.buffer + strlen("USER WRITE LOCAL");
    if ((ccode = GrabArgument(session, &ptr, &userDn)) == STATUS_CONTINUE) {
        if ((ccode = GrabArgument(session, &ptr, &attribute)) == STATUS_CONTINUE) {
            if (*ptr == ' ') {
                count = atol(ptr + 1);
                ccode = STATUS_IDENTITY_STORE_FAILURE;




            }
            MemFree(attribute);
        }
        MemFree(userDn);
    }
    return(SendError(session->client.conn, session->command.tag, "USER WRITE LOCAL", ccode));
}

int
ImapCommandUserWriteSuper(void *param)
{
    return(STATUS_UNKNOWN_COMMAND);
}

int
ImapCommandUserEnum(void *param)
{
    return(STATUS_UNKNOWN_COMMAND);
}

#endif

