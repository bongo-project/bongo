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

#ifndef _BONGO_CONNECTION_MANAGER_H
#define _BONGO_CONNECTION_MANAGER_H

#define CM_HASH_SIZE 128
#define CM_PORT 689 /* Shared with NMAP, since Connection Manager uses UDP */

/*
 * Events that require verification
 */
#define CM_EVENT_CONNECT        "CONNECT"       /* Is this address allowed to connect?              */

#define CM_EVENT_RELAY          "RELAY"         /* Is this address allowed to relay?  User will be  *
                                                 * filled out if a user is allowed to relay.        */

/*
 * Events that are notifications only
 */

#define CM_EVENT_DISCONNECTED   "DISCONNECTED"  /* Inform the connection manager of a disconnect    */

#define CM_EVENT_AUTHENTICATED  "AUTHENTICATED" /* Inform the connection manager that a user has    *
                                                 * authenticated.  detail.user should contain the   *
                                                 * name of the user.                                */

#define CM_EVENT_DELIVERED      "DELIVERED"     /* Inform the connection manager that a message has *
                                                 * been delivered.  detail.delivered should be      *
                                                 * filled out.                                      */

#define CM_EVENT_RECEIVED       "RECEIVED"      /* Inform the connection manager that a message has *
                                                 * been received.  detail.received should be filled *
                                                 * out.                                             */

#define CM_EVENT_VIRUS_FOUND    "VIRUS FOUND"   /* Inform the connection manager that a virus has   *
                                                 * been found.  detail.virus can be filled out with *
                                                 * the recipient count, and an optional name of the *
                                                 * virus.                                           */

#define CM_EVENT_SPAM_FOUND     "SPAM FOUND"    /* Inform the connection manager that a spam        *
                                                 * message has been found.  detail.spam can be      *
                                                 * filled to show the recipient count.              */

#define CM_EVENT_DOS_DETECTED   "DOS DETECTED"  /* Inform the connection manager that a DOS attack  *
                                                 * has been detected.  detail.dos.description can   *
                                                 * be given.                                        */


typedef struct _ConnMgrCommand {
    unsigned char   pass[32 + 1];           /* If left out, or incorrect the result will provide a salt for authentication. */
    unsigned long   command;                /* The ID of the command.                                                       */
    unsigned long   address;                /* The address of the connection.                                               */
    unsigned char   identity[16 + 1];       /* The identity of the calling agent.                                           */
    unsigned char   event[16 + 1];          /* A short text description of the event                                        */
    union {                                 /* The arguments for the given event.  If it is a non standard event use data   */
        struct {
            unsigned char user[MAXEMAILNAMESIZE + 1];
        } authenticated;

        struct {
            unsigned long recipients;
        } delivered;

        struct {
            unsigned long local;
            unsigned long invalid;
            unsigned long remote; 
        } received;

        struct {
            unsigned long recipients;
            unsigned char name[256 + 1];
        } virus;

        struct {
            unsigned long recipients;
        } spam;

        struct {
            unsigned char description[256 + 1];
        } dos;

        unsigned char data[256];

	struct {
	    /* what should the answer default to? should be a CM_RESULT */
	    unsigned long default_result;
	} policy;
    } detail;
} ConnMgrCommand;

typedef struct _ConnMgrResult {
    unsigned long result;
    unsigned char comment[128 + 1];
    union {
        unsigned char user[MAXEMAILNAMESIZE + 1];
        unsigned char salt[256 + 1];

        struct {
            BOOL requireAuth;
        } connect;
    } detail;
} ConnMgrResult;

#define CM_COMMAND_VERIFY               1
#define CM_COMMAND_NOTIFY               2

#define CM_RESULT_OK                    0
#define CM_RESULT_DENY_TEMPORARY        1
#define CM_RESULT_DENY_PERMANENT        2
#define CM_RESULT_ALLOWED               3
#define CM_RESULT_MUST_AUTH             4

#define CM_RESULT_FIRST_ERROR           100 /* All CM_RESULT errors must be >= CM_RESULT_FIRST_ERROR */
#define CM_RESULT_INVALID_AUTH          100
#define CM_RESULT_UNKNOWN_COMMAND       101
#define CM_RESULT_DBERR                 102
#define CM_RESULT_SHUTTING_DOWN         103
#define CM_RESULT_RECEIVER_DISABLED     104
#define CM_RESULT_CONNECTION_LIMIT      105
#define CM_RESULT_NO_MEMORY             106
#define CM_RESULT_UNKNOWN_ERROR         200


/*
    Module Callback Results
    -----------------------

    The result of a module's verify function should be a combination of the following
    bits or'ed together.  A result of 0 means the module does not care about the
    request.

    CM_MODULE_ACCEPT        - Accept the verify request
    CM_MODULE_DENY          - Deny the verify request
    CM_MODULE_FORCE         - Do not allow other modules to change the result
    CM_MODULE_PERMANENT     - Inform the calling agent that the result is permananent
*/

#define CM_MODULE_ACCEPT       (1 << 0)
#define CM_MODULE_DENY         (1 << 1)
#define CM_MODULE_FORCE        (1 << 2)
#define CM_MODULE_PERMANENT    (1 << 3)

typedef	BOOL (*CMShutdownFunc)();
typedef BOOL (*CMNotifyFunc)(ConnMgrCommand *command);
typedef int (*CMVerifyFunc)(ConnMgrCommand *command, ConnMgrResult *result);

typedef struct _CMModuleRegistrationStruct {
    int                     priority;

    CMShutdownFunc          Shutdown;
    CMNotifyFunc            Notify;
    CMVerifyFunc            Verify;
} CMModuleRegistrationStruct;

/*
    Each module must implement this function, with a name of MODULEInit where
    MODULE is the all upper case name of the module.
*/
typedef	BOOL (*CMInitFunc)(CMModuleRegistrationStruct *registration, unsigned char *dataDirectory);

#endif /* _BONGO_CONNECTION_MANAGER_H */
