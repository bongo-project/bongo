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

#include <memmgr.h>

#include <cmlib.h>
#include <connmgr.h>
#include <msgapi.h>

struct {
    unsigned char access[CM_HASH_SIZE];
    unsigned char pass[XPLHASH_MD5_LENGTH];
    MDBHandle directoryHandle;

    BOOL debug;
    unsigned long address;

    XplRWLock lock;
    unsigned char service[16 + 1];
} CMLibrary = {
    { '\0' }, 
    { '\0' }, 
    NULL, 

    FALSE
};

BOOL
CMSendCommand(ConnMgrCommand *command, ConnMgrResult *result)
{
    struct sockaddr_in fromSin;
    struct sockaddr_in toSin;
    int sock;
    int tries = 0;

    do {
        result->result = CM_RESULT_UNKNOWN_ERROR;

        XplRWReadLockAcquire(&CMLibrary.lock);
        strcpy(command->pass, CMLibrary.pass);
        XplRWReadLockRelease(&CMLibrary.lock);

        memset(&fromSin, 0, sizeof(struct sockaddr_in));
        memset(&toSin, 0, sizeof(struct sockaddr_in));
        sock = IPsocket(PF_INET, SOCK_DGRAM, IPPROTO_UDP);
        if (sock >= 0) {
            toSin.sin_family = AF_INET;
            toSin.sin_addr.s_addr = CMLibrary.address;
            toSin.sin_port = htons(CM_PORT);

            fromSin.sin_family = AF_INET;
            fromSin.sin_addr.s_addr = 0;
            fromSin.sin_port = 0;

            if (IPbind(sock, (struct sockaddr *)&fromSin, sizeof(fromSin)) == 0) {
                sendto(sock, (unsigned char *)command, sizeof(ConnMgrCommand), 0, (struct sockaddr*)&toSin, sizeof(toSin));
                XplIPRead(XPL_INT_TO_PTR(sock), (unsigned char *)result, sizeof(ConnMgrResult), 19);
            }
            IPclose(sock);
        }

        if (result->result == CM_RESULT_MUST_AUTH) {
            xpl_hash_context ctx;

            // Ensure that we don't walk off the edge of the buffer if garbage was sent
            result->detail.salt[sizeof(result->detail.salt) - 1] = '\0';

            XplHashNew(&ctx, XPLHASH_MD5);
            XplHashWrite(&ctx, result->detail.salt, strlen(result->detail.salt));
            XplHashWrite(&ctx, CMLibrary.access, CM_HASH_SIZE);
            XplHashFinal(&ctx, XPLHASH_LOWERCASE, command->pass, XPLHASH_MD5_LENGTH);

            /* Store the password so we don't have to generate it every time */
            XplRWWriteLockAcquire(&CMLibrary.lock);
            strcpy(CMLibrary.pass, command->pass);
            XplRWWriteLockRelease(&CMLibrary.lock);
        } else {
            return(TRUE);
        }
    } while (tries++ < 3);

    return(FALSE);
}

int
CMVerifyConnect(unsigned long address, unsigned char *comment, BOOL *requireAuth)
{
    ConnMgrCommand command;
    ConnMgrResult result;

    memset(&command, 0, sizeof(command));
    memset(&result, 0, sizeof(result));

    command.command = CM_COMMAND_VERIFY;
    strcpy(command.event, CM_EVENT_CONNECT);
    command.address = address;
    command.detail.policy.default_result = CM_RESULT_ALLOWED;

    CMSendCommand(&command, &result);

    if (result.comment[0] != '\0') {
        strncpy(comment, result.comment, sizeof(result.comment));
    } else {
        comment[0] = '\0';
    }

    if (requireAuth) {
        *requireAuth = result.detail.connect.requireAuth;
    }

    return(result.result);
}

int
CMVerifyRelay(unsigned long address, unsigned char *user)
{
    ConnMgrCommand command;
    ConnMgrResult result;

    memset(&command, 0, sizeof(command));
    memset(&result, 0, sizeof(result));

    command.command = CM_COMMAND_VERIFY;
    strcpy(command.event, CM_EVENT_RELAY);
    command.address = address;
    command.detail.policy.default_result = CM_RESULT_MUST_AUTH;

    CMSendCommand(&command, &result);

    if (result.result == CM_RESULT_ALLOWED && user) {
        strncpy(user, result.detail.user, MAXEMAILNAMESIZE);
    } else {
        user[0] = '\0';
    }

    return(result.result);
}

int
CMDisconnected(unsigned long address)
{
    ConnMgrCommand command;
    ConnMgrResult result;

    memset(&command, 0, sizeof(command));
    memset(&result, 0, sizeof(result));

    command.command = CM_COMMAND_NOTIFY;
    strcpy(command.event, CM_EVENT_DISCONNECTED);
    command.address = address;

    CMSendCommand(&command, &result);
    return(result.result);
}

int
CMAuthenticated(unsigned long address, unsigned char *user)
{
    ConnMgrCommand command;
    ConnMgrResult result;

    memset(&command, 0, sizeof(command));
    memset(&result, 0, sizeof(result));

    command.command = CM_COMMAND_NOTIFY;
    strcpy(command.event, CM_EVENT_AUTHENTICATED);
    command.address = address;

    if (user) {
        strncpy(command.detail.authenticated.user, user, MAXEMAILNAMESIZE);
    } else {
        command.detail.authenticated.user[0] = '\0';
    }

    CMSendCommand(&command, &result);
    return(result.result);
}

int
CMDelivered(unsigned long address, unsigned long recipients)
{
    ConnMgrCommand command;
    ConnMgrResult result;

    memset(&command, 0, sizeof(command));
    memset(&result, 0, sizeof(result));

    command.command = CM_COMMAND_NOTIFY;
    strcpy(command.event, CM_EVENT_DELIVERED);
    command.address = address;
    command.detail.delivered.recipients = recipients;

    CMSendCommand(&command, &result);
    return(result.result);
}

int
CMReceived(unsigned long address, unsigned long local, unsigned long invalid, unsigned long remote)
{
    ConnMgrCommand command;
    ConnMgrResult result;

    memset(&command, 0, sizeof(command));
    memset(&result, 0, sizeof(result));

    command.command = CM_COMMAND_NOTIFY;
    strcpy(command.event, CM_EVENT_RECEIVED);
    command.address = address;

    command.detail.received.local = local;
    command.detail.received.invalid = invalid;
    command.detail.received.remote = remote;

    CMSendCommand(&command, &result);
    return(result.result);
}

int
CMFoundVirus(unsigned long address, unsigned long recipients, unsigned char *name)
{
    ConnMgrCommand command;
    ConnMgrResult result;

    memset(&command, 0, sizeof(command));
    memset(&result, 0, sizeof(result));

    command.command = CM_COMMAND_NOTIFY;
    strcpy(command.event, CM_EVENT_VIRUS_FOUND);
    command.address = address;
    command.detail.virus.recipients = recipients;

    if (name) {
        strncpy(command.detail.virus.name, name, 256);
    } else {
        command.detail.virus.name[0] = '\0';
    }

    CMSendCommand(&command, &result);
    return(result.result);
}

int
CMSpamFound(unsigned long address, unsigned long recipients)
{
    ConnMgrCommand command;
    ConnMgrResult result;

    memset(&command, 0, sizeof(command));
    memset(&result, 0, sizeof(result));

    command.command = CM_COMMAND_NOTIFY;
    strcpy(command.event, CM_EVENT_SPAM_FOUND);
    command.address = address;
    command.detail.spam.recipients = recipients;

    CMSendCommand(&command, &result);
    return(result.result);
}

int
CMDOSDetected(unsigned long address, unsigned char *description)
{
    ConnMgrCommand command;
    ConnMgrResult result;

    memset(&command, 0, sizeof(command));
    memset(&result, 0, sizeof(result));

    command.command = CM_COMMAND_NOTIFY;
    strcpy(command.event, CM_EVENT_DOS_DETECTED);
    command.address = address;

    if (description) {
        strncpy(command.detail.dos.description, description, 256);
    } else {
        command.detail.dos.description[0] = '\0';
    }

    CMSendCommand(&command, &result);
    return(result.result);
}

BOOL 
CMInitialize(MDBHandle directoryHandle, unsigned char *service)
{
    BOOL result = FALSE;

    if (directoryHandle) {
        MDBValueStruct *v;

        CMLibrary.directoryHandle = directoryHandle;

        v = MDBCreateValueStruct(CMLibrary.directoryHandle, NULL);
        if (v) {
            MDBRead(MSGSRV_ROOT, MSGSRV_A_ACL, v);
            if (v->Used) {
                result = HashCredential(MsgGetServerDN(NULL), v->Value[0], CMLibrary.access);
            }

            MDBFreeValues(v);

            if (MsgReadIP((unsigned char *) MsgGetServerDN(NULL), MSGSRV_A_CONNMGR, v)) {
                CMLibrary.address = inet_addr(v->Value[0]);
            }

            MDBDestroyValueStruct(v);
        }

        XplRWLockInit(&CMLibrary.lock);
    }

    if (service) {
        strncpy(CMLibrary.service, service, 16);
        CMLibrary.service[16] = '\0';
    } else {
        CMLibrary.service[0] = '\0';
    }

    return(result);
}
