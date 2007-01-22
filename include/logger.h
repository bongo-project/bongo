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

#ifndef LOGGER_H
#define LOGGER_H

#include <xpl.h>

#include <log4c.h>

/* Subsystem constants */
#define	LOGGER_SUBSYSTEM_AUTH          "\\Authentication"
#define	LOGGER_SUBSYSTEM_GENERAL       "\\General"
#define	LOGGER_SUBSYSTEM_QUEUE         "\\Queue"
#define	LOGGER_SUBSYSTEM_UNHANDLED     "\\Unhandled Request"
#define	LOGGER_SUBSYSTEM_NOTIFY        "\\Notification"
#define	LOGGER_SUBSYSTEM_STANDARDS     "\\Standards Violation"
#define	LOGGER_SUBSYSTEM_MEMMON        "\\Memory Monitor"
#define	LOGGER_SUBSYSTEM_DATABASE      "\\Database"
#define	LOGGER_SUBSYSTEM_CONFIGURATION "\\Configuration"
#define	LOGGER_SUBSYSTEM_CALENDAR      "\\Calendar"
#define	LOGGER_SUBSYSTEM_MAIL          "\\Mail"

/* Event IDs */
typedef enum {
	LOGGER_EVENT_WRONG_PASSWORD							=	0x00020001,
	LOGGER_EVENT_UNHANDLED_REQUEST,						/*	0x00020002	*/
	LOGGER_EVENT_LOGIN,										/*	0x00020003	*/
	LOGGER_EVENT_MAX_FAILED_LOGINS, 						/*	0x00020004	*/
	LOGGER_EVENT_DISABLED_FEATURE, 						/*	0x00020005	*/
	LOGGER_EVENT_UNKNOWN_USER, 							/*	0x00020006	*/
	LOGGER_EVENT_NMAP_UNAVAILABLE, 						/*	0x00020007	*/
	LOGGER_EVENT_OUT_OF_MEMORY, 							/*	0x00020008	*/
	LOGGER_EVENT_NOT_CONFIGURED, 							/*	0x00020009	*/
	LOGGER_EVENT_SESSION_LIMIT_REACHED, 				/*	0x0002000A	*/
	LOGGER_EVENT_PASSWORD_CHANGE, 						/*	0x0002000B	*/
	LOGGER_EVENT_SECRET_CHANGE, 							/*	0x0002000C	*/
	LOGGER_EVENT_ACCOUNT_CREATED, 						/*	0x0002000D	*/
	LOGGER_EVENT_ACCOUNT_DISABLED, 						/*	0x0002000E	*/
	LOGGER_EVENT_MEMORY_LEAKED, 							/*	0x0002000F	*/
	LOGGER_EVENT_NULL_DESTINATION,						/*	0x00020010	*/
	LOGGER_EVENT_MEMORY_BOUNDARY_OVERRUN,				/*	0x00020011	*/
	LOGGER_EVENT_MEMORY_NON_OWNER_FREE, 				/*	0x00020012	*/
	LOGGER_EVENT_QUESTIONABLE_FREE, 						/*	0x00020013	*/
	LOGGER_EVENT_QUESTIONABLE_ALLOC, 					/*	0x00020014	*/
	LOGGER_EVENT_MESSAGE_RECEIVED, 						/*	0x00020015	*/
	LOGGER_EVENT_MESSAGE_SENT, 							/*	0x00020016	*/
	LOGGER_EVENT_CONNECTION_BLOCKED, 					/*	0x00020017	*/
	LOGGER_EVENT_RECIPIENT_BLOCKED, 						/*	0x00020018	*/
	LOGGER_EVENT_MESSAGE_RELAYED, 						/*	0x00020019	*/
	LOGGER_EVENT_RECIPIENT_LIMIT_REACHED,				/*	0x0002001A	*/
	LOGGER_EVENT_CONNECTION, 								/*	0x0002001B	*/
	LOGGER_EVENT_REGISTER_NMAP_SERVER, 					/*	0x0002001C	*/
	LOGGER_EVENT_AVIRUS_RESTART_FAILED,					/*	0x0002001D	*/
	LOGGER_EVENT_DUPLICATE_ALIAS,							/*	0x0002001E	*/
	LOGGER_EVENT_DATABASE_OPEN_ERROR,					/*	0x0002001F	*/
	LOGGER_EVENT_ALIAS_CONTEXTS,							/*	0x00020020	*/
	LOGGER_EVENT_RECEIVED_BOUNCE,							/*	0x00020021	*/
	LOGGER_EVENT_SPAM_BLOCKED,								/* 0x00020022	*/
	LOGGER_EVENT_VIRUS_BLOCKED,							/*	0x00020023	*/
	LOGGER_EVENT_FORWARD,									/*	0x00020024	*/
	LOGGER_EVENT_AUTO_REPLY,								/*	0x00020025	*/
	LOGGER_EVENT_RECEIVED_CONTROL,						/*	0x00020026	*/
	LOGGER_EVENT_PROCESSING_ERROR,						/*	0x00020027	*/
	LOGGER_EVENT_LIST_SEND_BLOCKED,						/*	0x00020028	First numeric value is one of ListBlockCodes	*/
	LOGGER_EVENT_CONFIGURATION_STRING,					/*	0x00020029	*/
	LOGGER_EVENT_CONFIGURATION_NUMERIC,					/*	0X0002002A	*/
	LOGGER_EVENT_QLIMITS_ADJUSTED,						/*	0x0002002B	*/
	LOGGER_EVENT_SSL_CONNECTION, 							/*	0x0002002C	*/
	LOGGER_EVENT_LIST_MESSAGE_RECEIVED,					/*	0x0002002D	*/
	LOGGER_EVENT_LIST_MESSAGE_DELIVERED,				/*	0x0002002E	*/
	LOGGER_EVENT_RELOADED_QUEUE_AGENT,					/*	0x0002002F	*/
	LOGGER_EVENT_PQE_FROM,									/*	0x00020030	*/
	LOGGER_EVENT_OVER_QUOTA,								/*	0x00020031	*/
	LOGGER_EVENT_BONGO_DEBUG,							/*	0x00020032	*/
	LOGGER_EVENT_WRONG_SYSTEM_AUTH,						/*	0x00020033	*/
	LOGGER_EVENT_SWITCH_USER_DENIED,						/*	0x00020034	*/
	LOGGER_EVENT_WRONG_USER_AUTH,							/*	0x00020035	*/
	LOGGER_EVENT_DISKSPACE_LOW,							/*	0x00020036	*/
	LOGGER_EVENT_WRITE_ERROR,								/*	0x00020037	*/
	LOGGER_EVENT_ADD_QUEUE_AGENT,							/*	0x00020038	*/
	LOGGER_EVENT_BAD_HOSTNAME,								/*	0x00020039	*/
	LOGGER_EVENT_PROXY_LOCALHOST,							/*	0x0002003A	*/
	LOGGER_EVENT_NMAP_ERROR,								/*	0x0002003B	*/
	LOGGER_EVENT_DDB_INIT_FAILED,							/*	0x0002003C	*/
	LOGGER_EVENT_DISALLOWED_HOSTNAME,					/*	0x0002003D	*/
	LOGGER_EVENT_EXECUTING_RULE,							/*	0x0002003E	*/
	LOGGER_EVENT_RULE_DELETE,								/*	0x0002003F	*/
	LOGGER_EVENT_RULE_FORWARD,								/*	0x00020040	*/
	LOGGER_EVENT_RULE_COPY,									/*	0x00020041	*/
	LOGGER_EVENT_RULE_MOVE,									/*	0x00020042	*/
	LOGGER_EVENT_REREGISTER_QUEUE_AGENT,				/*	0x00020043	*/
	LOGGER_EVENT_REMOVED_QUEUE_AGENT,					/*	0x00020044	*/
	LOGGER_EVENT_DATABASE_FIND_ERROR,					/*	0x00020045	*/
	LOGGER_EVENT_DATABASE_ALIAS_ERROR,					/*	0x00020046	*/
	LOGGER_EVENT_DATABASE_CLOSE_ERROR,					/*	0x00020047	*/
	LOGGER_EVENT_ITEM_STORED,								/*	0x00020048	*/
	LOGGER_EVENT_DATABASE_INSERT_ERROR,					/*	0x00020049	*/
	LOGGER_EVENT_DATABASE_CREATE_ERROR,					/*	0x0002004A	*/
	LOGGER_EVENT_PQE_START,									/*	0x0002004B	*/
	LOGGER_EVENT_PQE_FIND_OBJECT_FAILED,				/*	0x0002004C	*/
	LOGGER_EVENT_PQE_REMOTE_NMAP,							/*	0x0002004D	*/
	LOGGER_EVENT_PQE_DELIVERY_FAILED,					/*	0x0002004E	*/
	LOGGER_EVENT_PQE_FAILED_CONNECT,						/*	0x0002004F	*/
	LOGGER_EVENT_PQE_FAILED,								/*	0x00020050	*/
	LOGGER_EVENT_MISSING_STRINGS_FILE,					/*	0x00020051	*/
	LOGGER_EVENT_WRONG_STRING_COUNT,						/*	0x00020052	*/
	LOGGER_EVENT_DIGEST_SENT,								/*	0x00020053	*/
	LOGGER_EVENT_CREATE_SOCKET_FAILED,					/*	0x00020054	*/
	LOGGER_EVENT_CARRIERSCAN_CONNECT_FAILED,			/*	0x00020055	*/
	LOGGER_EVENT_ILLEGAL_EMAIL_ADDRESS,					/*	0x00020056	*/
	LOGGER_EVENT_OPEN_STRINGS_FILE,						/*	0x00020057	*/
	LOGGER_EVENT_MESSAGE_TRY_LATER,						/*	0x00020058	*/
	LOGGER_EVENT_MESSAGE_FAILED_DELIVERY,				/*	0x00020059	*/
	LOGGER_EVENT_MEMORY_WILL_OVERRUN,					/*	0x0002005A	*/
	LOGGER_EVENT_NULL_SOURCE,								/*	0x0002005B	*/
	LOGGER_EVENT_ADD_TO_BLOCK_LIST,						/*	0x0002005C	*/
	LOGGER_EVENT_ACCEPT_FAILURE,							/*	0x0002005D	*/
	LOGGER_EVENT_INDEX_CORRUPTION,						/*	0x0002005E	*/
	LOGGER_EVENT_NMAP_OUT_OF_MEMORY,						/*	0x0002005F	*/
	LOGGER_EVENT_FILE_OPEN_FAILURE,						/*	0x00020060	*/
	LOGGER_EVENT_FILE_IO_FAILURE,							/*	0x00020061	*/
	LOGGER_EVENT_CONNECTION_TIMEOUT,						/*	0x00020062	*/
	LOGGER_EVENT_CONNECTION_ERROR,						/*	0x00020063	*/
	LOGGER_EVENT_PQE_LOCK_FULL,							/*	0x00020064	*/
	LOGGER_EVENT_AGENT_HEARTBEAT,							/*	0x00020065	*/
	LOGGER_EVENT_MBOX_CORRUPTION,							/*	0x00020066	*/
	LOGGER_EVENT_PROXY_HEARTBEAT,							/*	0x00020067	*/
	LOGGER_EVENT_CONNECTION_LOCAL,						/*	0x00020068	*/
	LOGGER_EVENT_DNS_CONFIGURATION_ERROR,				/*	0x00020069	*/
	LOGGER_EVENT_MESSAGE_WITH_UNKNOWN_RECIPS,			/*	0x0002006A	*/
	LOGGER_EVENT_IP_CONNECT_FAILURE,						/*	0x0002006B	*/
	LOGGER_EVENT_SMTP_CONNECT_ERROR,						/*	0x0002006C	*/
    LOGGER_EVENT_NEGOTIATION_FAILED, /* 0x0002006D */
    LOGGER_EVENT_NETWORK_IO_FAILURE, /* 0x0002006E */
    LOGGER_EVENT_PROXY_USER, /* 0x0002006F */
    LOGGER_EVENT_SERVER_SHUTDOWN, /* 0x00020070 */
    LOGGER_EVENT_PRIV_FAILURE, /* 0x00020071 */
    LOGGER_EVENT_BIND_FAILURE, /* 0x00020072 */

	/*
		Third-party Bongo Queue Agent event ID's.

															0x00028000
	*/

	LOGGER_EVENT_LAST_EVENT_ID
} LoggerEventIDs;

/* List Codes */
typedef enum {
	LOGGER_LIST_NO_AUTH = 1, 
	LOGGER_LIST_NOT_MODERATOR, 
	LOGGER_LIST_NO_ATTACHMENTS, 
	LOGGER_LIST_USER_BANNED, 

	LOGGER_LIST_LAST_CODE
} LoggerListCodes;

/* Blocking Codes */
typedef enum {
	LOGGING_BLOCK_REVERSE_FAILED = 1, 
	LOGGING_BLOCK_BLOCKLIST, 
	LOGGING_BLOCK_RBL, 

	LOGGER_LAST_BLOCK_CODE
} LoggerBlockCodes;

/* Variables */

typedef struct _LoggingHandle LoggingHandle;

/* Prototypes */
EXPORT LoggingHandle *LoggerOpen(const char *name);
EXPORT void LoggerClose(LoggingHandle *handle);
EXPORT void LoggerEvent(LoggingHandle *handle, const char *subsystem, unsigned long eventId, int level, int unknown /*fixme*/, const char *str1, const char *str2, int i1, int i2, void *p, int size);

#endif /* LOGGER_H */
