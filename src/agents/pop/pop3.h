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

/* Port to listen */
#define POP3_PORT 110
#define POP3_PORT_SSL 995

/* Default INBOX name for NMAP */
#define INBOX_NAME "INBOX"

#define UIDLLEN 33

#define POP3_MSG_STATE_NORMAL (1 << 0)
#define POP3_MSG_STATE_DELETED (1 << 1)
#define POP3_MSG_STATE_NEWUIDL (1 << 2)

#define MSGOK "+OK\r\n"
#define MSGOKHELLO "Bongo POP3 server ready\r\n"
#define MSGOKBYE "signing off"
#define MSGCAPALIST "+OK\r\nTOP\r\nUSER\r\nSASL LOGIN\r\nRESP-CODES\r\nAUTH-RESP-CODE\r\nPIPELINING\r\nEXPIRE NEVER\r\nUIDL\r\nSTLS\r\n.\r\n"
#define MSGCAPALIST_NOTLS "+OK\r\nTOP\r\nUSER\r\nSASL LOGIN\r\nRESP-CODES\r\nAUTH-RESP-CODE\r\nPIPELINING\r\nEXPIRE NEVER\r\nUIDL\r\n.\r\n"
#define MSGWRONGMETHOD "-ERR [AUTH] Unsupported method\r\n"
#define MSGOKPASSWD "+OK Password required\r\n"
#define MSGOKSTARTTLS "+OK Begin TLS negotiations\r\n"
#define MSGERRNONMAP "Bongo POP3 server not ready (can't contact NMAP)"
#define MSGERRDUPNAME "-ERR [SYS/TEMP] I already know who you are!\r\n"
#define MSGERRBOXLOCKED "-ERR [IN-USE] Mailbox is already locked. POP access denied\r\n"
#define MSGERRREADONLY "-ERR [SYS/PERM] Mailbox is read-only\r\n"
#define MSGERRNOBOX "-ERR [AUTH] Mailbox doesn't exist\r\n"
#define MSGERRNOMSG "-ERR [SYS/PERM] Message does not exist\r\n"
#define MSGERRMSGDELETED "-ERR [SYS/PERM] Message is marked deleted\r\n"
#define MSGERRNMAP "-ERR [SYS/PERM] NMAP returned:"
#define MSGERRUNKNOWN "-ERR [SYS/PERM] Unknown command\r\n"
#define MSGERRBADSTATE "-ERR [SYS/PERM] Command issued in wrong state\r\n"
#define MSGERRSYNTAX "-ERR [SYS/PERM] Syntax error\r\n"
#define MSGERRNOPERMISSION "-ERR [AUTH] Permission denied\r\n"
#define MSGERRNOPERMISSIONCLOSING "-ERR [AUTH] Permission denied, maximum login attempts exceeded, connection closing\r\n"
#define MSGERRINTERNAL "-ERR [SYS/PERM] Internal error\r\n"
#define MSGERRNOTSUPPORTED "-ERR [SYS/PERM] Command not supported\r\n"
#define MSGERRNOMEMORY "-ERR [SYS/TEMP] Out of memory\r\n"
#define MSGERRNOCONNECTIONS "-ERR [SYS/TEMP] many connections, try later\r\n"
#define MSGERRNORIGHTS "-ERR [AUTH] POP Access for account disabled\r\n"
#define MSGRECEIVERDOWN "-ERR [SYS/PERM] POP access to this server currently disabled\r\n"
#define MSGNOCONNECTIONS "-ERR [SYS/TEMP] POP connection limit for this server reached\r\n"
#define MSGSHUTTINGDOWN "-ERR [SYS/PERM] Bongo POP3 server shutting down\r\n"
