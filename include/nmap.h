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

#ifndef _NMAD_H_
#define _NMAD_H_

#include "xpl.h"
#include <time.h>
#include <gnutls/openssl.h>

#define SHA_DIGEST_LENGTH	20

#define	NMAP_PORT		689
#define BONGO_QUEUE_PORT 8670   
#define	NMAP_SSL_PORT	1001
#define	DEFAULT_INDEX_PORT 8689
#define	NMAP_HASH_SIZE	128
#define	UIDLLEN			33
#define	NMAP_IDX_LINE_TAIL_SIGNATURE		0x05FACE50

/* Minutes * 60 (1 second granularity) */
#define	NMAP_CONNECTION_TIMEOUT	(45*60)

/* Descriptor of message, format of .IDX file */
#define NMAP_MAIL_EXT ".box"
#define NMAP_MAIL_INDEX_EXT ".dbm"
#define NMAP_MAIL_INDEX_VERSION 1

#define	NMAP_IDX_VERSION				"NIMSv04"
#define	NMAP_IDX_HEADERSIZE			69			/* 7 + 5*10 + 6*2; 7 for the version plus 5*10 for data plus 6*2 for \r\n	*/

#define NMAP_DSTORE_VERSION 1

#define NMAP_GUID_PREFIX_LENGTH (SHA_DIGEST_LENGTH * 2)
#define NMAP_GUID_SUFFIX_LENGTH 6
#define NMAP_GUID_LENGTH (NMAP_GUID_PREFIX_LENGTH + NMAP_GUID_SUFFIX_LENGTH)

#define NMAP_SQLTRAN_BEGIN "BEGIN IMMEDIATE TRANSACTION;"
#define NMAP_SQLTRAN_BEGIN_SHARED "BEGIN TRANSACTION;"
#define NMAP_SQLTRAN_END "END TRANSACTION;"
#define NMAP_SQLTRAN_ROLLBACK "ROLLBACK TRANSACTION;"

typedef unsigned int NmapCalID;

typedef enum _NmapDocumentTypeMasks {
    NMAP_DOCTYPE_MASK_UNKNOWN = 0x0001,
    NMAP_DOCTYPE_MASK_MAIL    = 0x0002,
    NMAP_DOCTYPE_MASK_CAL     = 0x0003,
    NMAP_DOCTYPE_MASK_AB      = 0x0004,

    NMAP_DOCTYPE_MASK_FOLDER  = 0x1000,
    NMAP_DOCTYPE_MASK_SHARED  = 0x2000,

    NMAP_DOCTYPE_MASK_SHARED_FOLDER = NMAP_DOCTYPE_MASK_SHARED | NMAP_DOCTYPE_MASK_FOLDER,

    NMAP_DOCTYPE_MASK_PURGED  = 0x4000,
} NmapDocumentTypeMasks;

typedef enum _NmapDocumentTypes {

    NMAP_DOCTYPE_UNKNOWN = NMAP_DOCTYPE_MASK_UNKNOWN,
    NMAP_DOCTYPE_MAIL    = NMAP_DOCTYPE_MASK_MAIL, 
    NMAP_DOCTYPE_CAL     = NMAP_DOCTYPE_MASK_CAL, 
    NMAP_DOCTYPE_AB      = NMAP_DOCTYPE_MASK_AB, 

    NMAP_DOCTYPE_FOLDER         = NMAP_DOCTYPE_MASK_FOLDER,
    NMAP_DOCTYPE_UNKNOWN_FOLDER = NMAP_DOCTYPE_MASK_FOLDER | NMAP_DOCTYPE_MASK_UNKNOWN, 
    NMAP_DOCTYPE_MAIL_FOLDER    = NMAP_DOCTYPE_MASK_FOLDER | NMAP_DOCTYPE_MASK_MAIL, 
    NMAP_DOCTYPE_CAL_FOLDER     = NMAP_DOCTYPE_MASK_FOLDER | NMAP_DOCTYPE_MASK_CAL,
    NMAP_DOCTYPE_AB_FOLDER      = NMAP_DOCTYPE_MASK_FOLDER | NMAP_DOCTYPE_MASK_AB, 

    NMAP_DOCTYPE_IN_SHARE         = NMAP_DOCTYPE_MASK_SHARED,
    NMAP_DOCTYPE_UNKNOWN_IN_SHARE = NMAP_DOCTYPE_MASK_SHARED | NMAP_DOCTYPE_MASK_UNKNOWN,
    NMAP_DOCTYPE_MAIL_IN_SHARE    = NMAP_DOCTYPE_MASK_SHARED | NMAP_DOCTYPE_MASK_MAIL,
    NMAP_DOCTYPE_CAL_IN_SHARE     = NMAP_DOCTYPE_MASK_SHARED | NMAP_DOCTYPE_MASK_CAL,
    NMAP_DOCTYPE_AB_IN_SHARE      = NMAP_DOCTYPE_MASK_SHARED | NMAP_DOCTYPE_MASK_AB,

    NMAP_DOCTYPE_SHARED_FOLDER         = NMAP_DOCTYPE_MASK_SHARED_FOLDER,
    NMAP_DOCTYPE_UNKNOWN_SHARED_FOLDER = NMAP_DOCTYPE_MASK_SHARED_FOLDER | NMAP_DOCTYPE_MASK_UNKNOWN,
    NMAP_DOCTYPE_MAIL_SHARED_FOLDER    = NMAP_DOCTYPE_MASK_SHARED_FOLDER | NMAP_DOCTYPE_MASK_MAIL,
    NMAP_DOCTYPE_CAL_SHARED_FOLDER     = NMAP_DOCTYPE_MASK_SHARED_FOLDER | NMAP_DOCTYPE_MASK_CAL,
    NMAP_DOCTYPE_AB_SHARED_FOLDER      = NMAP_DOCTYPE_MASK_SHARED_FOLDER | NMAP_DOCTYPE_MASK_AB,

    NMAP_DOCTYPE_PURGED = NMAP_DOCTYPE_MASK_PURGED,
} NmapDocumentTypes;



#define NMAP_IS_FOLDER(doctype) (doctype & 0x1000)
#define NMAP_IS_SHARED_FOLDER(doctype) (0x3000 == doctype & 0x3000)

/* Message states */
typedef enum _MessageStateFlags {
    MSG_STATE_READ = (1 << 0), 
    MSG_STATE_DELETED = (1 << 1), 
    MSG_STATE_NEWUIDL = (1 << 2), 
    MSG_STATE_ESCAPE = (1 << 3), 
    MSG_STATE_PRIOHIGH = (1 << 4), 
    MSG_STATE_PRIOLOW = (1 << 5), 
    MSG_STATE_PURGED = (1 << 6), 
    MSG_STATE_RECENT = (1 << 7), 
    MSG_STATE_ANSWERED = (1 << 8), 
    MSG_STATE_DRAFT = (1 << 9), 
    MSG_STATE_MARK_PURGE = (1 << 10), 
    MSG_STATE_COMPLETED = (1 << 11), 
    MSG_STATE_PRIVATE = (1 << 12), 

    /* do not include this message in free busy searches */
    MSG_STATE_NOFREEBUSY = (1 << 13), 

    MSG_STATE_USE_SCMS = (1 << 14), 
    MSG_STATE_MODIFIED = (1 << 15)
} MessageStateFlags;

typedef struct _NmapMailInfoStarts {
    unsigned long store;
    unsigned long properties;
    unsigned long auth;
    unsigned long from;
    unsigned long to;
    unsigned long subject;
    unsigned long body;
} NmapMailInfoStarts;

typedef struct _NmapMailInfoLengths {
    unsigned long store;
    unsigned long auth;
    unsigned long from;
    unsigned long to;
    unsigned long subject;
    unsigned long message;
} NmapMailInfoLengths;

typedef struct _NmapMailInfo {
    MessageStateFlags state; /* State UseSCMS */

    long bodyLines; /* BodyLines */
    long uid; /* UID */

    unsigned char guid[NMAP_GUID_LENGTH + 1];

    double modified;
    unsigned long key;
    unsigned long scmsID; /* SCMSID */

    time_t sent; /* DateSent */
    time_t received; /* DateReceived */

    NmapMailInfoStarts start;

    NmapMailInfoLengths length;
} NmapMailInfo;

typedef struct {
    long MSize;              /* Size of Message [Header+Body] */
    long SSize;              /* Size of message to be sent */
    long HeadPos;            /* Offset of Header-start */
    long BodyPos;            /* Offset of Body-start */
    long AuthPos;            /* Offset of Auth-string-start */
    unsigned int AuthLen;    /* Length of Auth-string */
    long BodyLines;          /* Lines in body */
    unsigned long State;     /* State of message, see below */
    char UIDL[UIDLLEN];      /* Hash of message */
    long UID;                /* folder unique id */
    time_t DateSent;         /* receive time, seconds since '70 */
    time_t DateReceived;     /* receive time, seconds since '70 */
    unsigned long UseSCMS;   /* Use Single Copy Message Store */
    unsigned long SCMSID;    /* ID of Single Copy */
    unsigned long RealStart; /* Maildrop message start pos */
    unsigned long RealSize;  /* Maildrop message size */
    unsigned long TailSig;   /* Must be NMAP_IDX_LINE_TAIL_SIGNATURE */
} MessageInfoStruct;

/*
	Calendar store index format, all times are always specified in UTC
*/
#define NMAP_CAL_EXT ".cav"
#define NMAP_CAL_INDEX_EXT ".dbc"
#define NMAP_CAL_INDEX_VERSION 1

#define	NMAP_CAL_IDX_VERSION			"CNIMSv05"
#define	NMAP_CAL_IDX_HEADERSIZE		70		/* 8 + 5*10 + 6*2; 7 for the version, plus 5*10 chars for the data plus 6*\r\n */

/* Calendar Flags */
typedef struct _NmapCalInfoStarts {
    unsigned long store;
} NmapCalInfoStarts;

typedef struct _NmapCalInfoLengths {
    unsigned long store;
} NmapCalInfoLengths;

typedef struct _NmapCalInfo {
    MessageStateFlags state;

    unsigned long key;
    long uid; /* UID */

    unsigned char guid[NMAP_GUID_LENGTH + 1];

    double modified;

    NmapCalInfoStarts start;
    NmapCalInfoLengths length;
} NmapCalInfo;

typedef struct {
	unsigned long	State;				/* Item state											*/
	unsigned long	UTCEnd;				/* End of event; seconds since 1970				*/
	unsigned long	UTCStart;			/* Start of event; seconds since 1970			*/
	unsigned long	UTCDue;				/* Duedate of event; seconds since 1970		*/
	unsigned char	Type;					/* Event type											*/
	unsigned long	Sequence;			/* Sequence number of event						*/
	unsigned long	UID;					/* Unique ID											*/
	unsigned char	ICalUID[40];		/* First 40 chars of iCal UID						*/
	unsigned char	Organizer[40];		/* First 40 chars of Organizer					*/
	unsigned char	Summary[40];		/* First 40 chars of Summary						*/
	BOOL				Recurring;			/* Is event recurring								*/
	unsigned long	RecurrenceID;		/* Unique Recurrence Identifier					*/
	unsigned long	UIDPos;				/* Start of UID line									*/
	unsigned long	OrganizerPos;		/* Start of organizer line							*/
	unsigned long	AttendeePos;		/* Start of attendeelist							*/
	unsigned long	AttendeeCount;		/* Number of attendees								*/
	unsigned long	AttendeeSize;		/* Length of attendeelist							*/
	unsigned long	Pos;					/* Start of entry in .CAL file					*/
	unsigned long	Size;					/* Total size of entry								*/
	unsigned long	CalSize;				/* Start of public entry in .CAL file			*/
	unsigned long	TailSig;				/*	Must be NMAP_IDX_LINE_TAIL_SIGNATURE	*/
} CalendarInfoStruct;

/* Calendar types */
#define	NMAP_CAL_EVENT				1
#define	NMAP_CAL_TODO				2
#define	NMAP_CAL_JOURNAL			3

/* Attendee parsing parameters */
#define	NMAP_CAL_ADDRESS_PREFIX		23
#define	NMAP_CAL_ADDRESS_POS		27

#define	NMAP_CAL_RSVP_POS			25
#define	NMAP_CAL_RSVP_YES			'Y'
#define	NMAP_CAL_RSVP_NO			'N'

#define	NMAP_CAL_ROLE_POS			24
#define	NMAP_CAL_ROLE_CHAIR		'C'
#define	NMAP_CAL_ROLE_REQUIRED	'R'
#define	NMAP_CAL_ROLE_OPTIONAL	'O'
#define	NMAP_CAL_ROLE_NOT			'N'

#define	NMAP_CAL_TYPE_POS				26
#define	NMAP_CAL_TYPE_INDIVIDUAL	'I'
#define	NMAP_CAL_TYPE_GROUP			'G'
#define	NMAP_CAL_TYPE_RESOURCE		'R'
#define	NMAP_CAL_TYPE_ROOM			'M'
#define	NMAP_CAL_TYPE_UNKNOWN		'U'

#define	NMAP_CAL_STATE_POS			23
#define	NMAP_CAL_STATE_NEED_ACT		'N'
#define	NMAP_CAL_STATE_ACCEPTED		'A'
#define	NMAP_CAL_STATE_DECLINED		'D'
#define	NMAP_CAL_STATE_TENTATIVE	'T'
#define	NMAP_CAL_STATE_DELEGATED	'G'
#define	NMAP_CAL_STATE_COMPLETED	'C'
#define	NMAP_CAL_STATE_INPROCESS	'I'

/* Maximum size for certain fields (MIME RESPONSE) */
#define	MIME_TYPE_LEN			31
#define	MIME_SUBTYPE_LEN		31
#define	MIME_NAME_LEN			(3 * XPL_MAX_PATH + 20)	/* rfc2231 encoding can cause paths to be more than 3 times max path */ 
#define	MIME_SEPARATOR_LEN	127
#define	MIME_CHARSET_LEN		63
#define	MIME_ENCODING_LEN		64

/* Client session flags */
#define	NMAP_SHOWDELETED		(1<<1)			/* Always show deleted messages									*/
#define	NMAP_OOBMESSAGES		(1<<2)			/* Send Out-Of-Band info about mailbox							*/
#define	NMAP_REVERSEORDER		(1<<3)			/* List messages in reverse order (Newest first)			*/
#define	NMAP_KEEPUSER			(1<<4)			/* Do not allow change of user									*/
#define	NMAP_OOBCONTACTS		(1<<5)			/* Send Out-Of-Band info about address book				*/
#define	NMAP_CSTORE_READONLY	(1<<29)			/* Readonly connection, client cannot change calendar		*/
#define	NMAP_SHARE_DENIED		(1<<30)			/*	Client no longer has permission to the selected share	*/
#define	NMAP_READONLY			(1<<31)			/* Readonly connection, client cannot change mailbox		*/

/* NMAP Asyncronous Events */
#define	EVENT_MBOX_NEW_MESSAGES		(1<<1)	/* New Messages have arrived									*/
#define	EVENT_MBOX_UNSEEN_PURGES	(1<<2)	/* Unseen Purges exist											*/
#define	EVENT_MBOX_FLAGS_CHANGED	(1<<3)	/* Message flags have changed; its id and flags follow*/
#define	EVENT_ADBK_NEW_CONTACTS		(1<<4)	/* New Contacts have arrived									*/

/*	Resource states (flags)	*/
#define	RESOURCE_FLAG_UNSUBSCRIBED	(1<<0)
#define	RESOURCE_FLAG_SHARED			(1<<1)
#define	RESOURCE_FLAG_SHARED_PUBLIC (1<<2)

/*	Shared resource permissions (flags)	*/
#define	NMAP_SHARE_VISIBLE		(1 << 0)
#define	NMAP_SHARE_SEEN			(1 << 1)
#define	NMAP_SHARE_READ			(1 << 2)
#define	NMAP_SHARE_WRITE			(1 << 3)
#define	NMAP_SHARE_INSERT			(1 << 4)
#define	NMAP_SHARE_CREATE			(1 << 5)
#define	NMAP_SHARE_DELETE			(1 << 6)
#define	NMAP_SHARE_ADMINISTER	(1 << 7)
#define	NMAP_SHARE_POST			(1 << 8)

/* Message flags */
typedef enum _NMAPMessageFlags {
    MSG_FLAG_ENCODING_NONE = (1 << 0), 
    MSG_FLAG_ENCODING_7BIT = (1 << 1), 
    MSG_FLAG_ENCODING_8BITM = (1 << 2), 
    MSG_FLAG_ENCODING_BINM = (1 << 3), 
    MSG_FLAG_ENCODING_BIN = (1 << 4), 
    MSG_FLAG_SOURCE_EXTERNAL = (1 << 5), 
    MSG_FLAG_CALENDAR_OBJECT = (1 << 6), 
    MSG_FLAG_PROXIED_MESSAGE = (1 << 7), /* Must always be 128 */
    MSG_FLAG_SPAM_CHECKED = (1 << 8), 
    MSG_FLAG_PLUSPACK_PROCESSED = (1 << 9), 
    MSG_FLAG_PLUSPACK_COPIED = (1 << 10)
} NMAPMessageFlags;

/* Queue "names" */
#define	Q_INCOMING				0
#define	Q_INCOMING_CLEAN		1
#define	Q_FORWARD				2
#define	Q_RULE					3
#define	Q_FOUR					4
#define	Q_FIVE					5
#define	Q_DELIVER				6
#define	Q_OUTGOING				7
#define	Q_RTS						8
#define	Q_DIGEST					9

/* Delivery Status Notification and other Control File Flags */
#define	DSN_FLAGS				(0x0000001F)
#define	DSN_DEFAULT				(DSN_FAILURE | DSN_HEADER | DSN_BODY)
#define	DSN_SUCCESS				(1<<0)
#define	DSN_TIMEOUT				(1<<1)
#define	DSN_FAILURE				(1<<2)
#define	DSN_HEADER				(1<<3)
#define	DSN_BODY					(1<<4)
#define	NO_FORWARD				(1<<5)
#define	NO_RULEPROCESSING		(1<<6)
#define	NO_CALENDARPROCESSING	(1<<7)
#define	NO_VIRUSSCANNING		(1<<8)
#define NO_PLUSPACK (1 << 9)

/* Delivery states */
#define	DELIVER_SUCCESS				1
#define	DELIVER_FAILURE				-1
#define	DELIVER_HOST_UNKNOWN			-2
#define	DELIVER_BOGUS_NAME			-3
#define	DELIVER_TIMEOUT				-4
#define	DELIVER_REFUSED				-5
#define	DELIVER_UNREACHABLE			-6
#define	DELIVER_LOCKED					-7
#define	DELIVER_USER_UNKNOWN			-8
#define	DELIVER_HOPCOUNT_EXCEEDED	-9
#define	DELIVER_TRY_LATER				-10
#define	DELIVER_INTERNAL_ERROR		-11
#define	DELIVER_TOO_LONG				-12
#define	DELIVER_PENDING				-13
#define	DELIVER_QUOTA_EXCEEDED		-14
#define	DELIVER_BLOCKED				-15
#define	DELIVER_PROCESSING_ERROR	-16
#define	DELIVER_AUTH_ERROR			-17
#define	DELIVER_VIRUS_REJECT			-18
#define DELIVER_UNKNOWN_TYPE                  -19

typedef int NMAPDeliveryCode;

/* Queue prefixes (Char) */
#define	QUEUE_ID						'I'
#define	QUEUE_BOUNCE				'B'
#define	QUEUE_FROM					'F'
#define	QUEUE_FLAGS					'X'
#define	QUEUE_RECIP_REMOTE		'R'
#define	QUEUE_RECIP_LOCAL			'L'
#define	QUEUE_DATE					'D'
#define	QUEUE_RECIP_MBOX_LOCAL	'M'
#define	QUEUE_CALENDAR_LOCAL		'C'
#define	QUEUE_ADDRESS				'A'
#define QUEUE_THIRD_PARTY 'T'

/* Queue prefixes (String) */
#define	QUEUES_ID					"I"
#define	QUEUES_BOUNCE				"B"
#define	QUEUES_FROM					"F"
#define	QUEUES_FLAGS				"X"
#define	QUEUES_RECIP_REMOTE		"R"
#define	QUEUES_RECIP_LOCAL		"L"
#define	QUEUES_DATE					"D"
#define	QUEUES_RECIP_MBOX_LOCAL	"M"
#define	QUEUES_CALENDAR_LOCAL	"C"
#define	QUEUES_ADDRESS				"A"
#define QUEUES_THIRD_PARTY "T"

/* Result codes */
#define	NMAP_READY					1000
#define	NMAP_OK						1000
#define	NMAP_BYE						1000
#define	NMAP_LOGIN_OK				1000
#define	NMAP_FLAGS_SET				1000
#define	NMAP_DELETE_OK				1000
#define	NMAP_ENTRY_REMOVED		1000
#define	NMAP_ENTRY_CREATED		1000
#define	NMAP_SPACE_RESULT_OK		1000
#define	NMAP_MBOX_RW_SELECTED	1000
#define	NMAP_MBOX_RO_SELECTED	1020
#define	NMAP_USER_SELECTED		1000
#define	NMAP_MBOX_CREATED			1000
#define	NMAP_INFORMATION_STORED	1000
#define	NMAP_QUEUE_STATE_OK		1000
#define	NMAP_PARAMETER_SET		1000
#define	NMAP_MESSAGES_PURGED		1000
#define	NMAP_MESSAGES_SALVAGED	1000
#define	NMAP_WATCHMODE_ENTERED	1000
#define	NMAP_WATCHMODE_RETURNED	1000
#define	NMAP_SUCCESS				2001
#define	NMAP_SUCCESS_MULTILINE	2002
#define	NMAP_MULTILINE_CONT		2003
#define	NMAP_MIMEPART_START		2002
#define	NMAP_MULTIPART_END		2003
#define	NMAP_RFC822_END			2004
#define	NMAP_UNKNOWN_COMMAND		3000
#define	NMAP_WRONG_ARGUMENTS		3010
#define	NMAP_WRONG_STATE			3012
#define NMAP_MBOX_LOCKED 4120
#define NMAP_CAL_LOCKED 4120
#define	NMAP_FAILED_READONLY		4243
#define	NMAP_AUTH_REQUIRED		4242
#define	NMAP_SERVER_FAILURE		5001
#define NMAP_SERVER_OUT_OF_MEMORY 5001
#define NMAP_SERVER_SHUTTING_DOWN 5002
#define NMAP_FILE_NOT_FOUND 4201
#define NMAP_FILE_READ_ERROR 4202
#define	NMAP_CALLBACK_DATA_START	6020
#define	NMAP_CALLBACK_DATA_END	6021

/* Disk Flush Points */
#define	FLUSH_BOX_ON_PURGE		(1<<0)
#define	FLUSH_BOX_ON_STORE		(1<<1)
#define	FLUSH_IDX_ON_PARSE		(1<<2)
#define	FLUSH_CAL_ON_PURGE		(1<<3)
#define	FLUSH_CAL_ON_STORE		(1<<4)
#define	FLUSH_IDC_ON_PARSE		(1<<5)


#endif
