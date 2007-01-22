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
#define SMTP_PORT			25
#define SMTP_PORT_SSL	465

/* Internal stuff */
#define	CR		0x0d
#define	LF		0x0a

/* Receiver States */
#define	STATE_FRESH		0
#define	STATE_HELO		1
#define	STATE_AUTH		2
#define	STATE_FROM		3
#define	STATE_TO			4
#define	STATE_DATA		5
#define	STATE_BDAT		6
#define	STATE_DONE		7
#define	STATE_ENDING	8
#define	STATE_WAITING	9

/* For RewriteAddress */
#define	MAIL_BOGUS		0
#define	MAIL_LOCAL		1
#define	MAIL_REMOTE		2
#define	MAIL_RELAY		3

/* SMTP Extensions */
#define	EXT_DSN			(1<<0)
#define	EXT_PIPELINING	(1<<1)
#define	EXT_8BITMIME	(1<<2)
#define	EXT_AUTH_LOGIN	(1<<3)
#define	EXT_CHUNKING	(1<<4)
#define	EXT_BINARYMIME	(1<<5)
#define	EXT_SIZE			(1<<6)
#define	EXT_ETRN			(1<<7)
#define	EXT_TLS			(1<<8)

/* Messages */
#define	MSG211HELP				"211-Acceptable commands\r\n211-HELO  MAIL  RCPT  DATA\r\n211-RSET  VRFY  EXPN  HELP\r\n211 NOOP  QUIT\r\n"
#define	MSG211HELP_LEN			97

#define	MSG220READY		"Bongo SMTP ready"

#define	MSG220TLSREADY		"220 Ready to start TLS\r\n"
#define	MSG220TLSREADY_LEN	24

#define	MSG221QUIT		"So long, and thanks for all the fish"

#define	MSG250OK		"250 OK\r\n"
#define	MSG250OK_LEN		8

#define	MSG250HELLO		"Pleased to meet you"
#define	MSG250HELLO_LEN		19

#define	MSG250ETRN		"250-ETRN\r\n"
#define	MSG250TLS		"250-STARTTLS\r\n"
#define	MSG250AUTH		"250-AUTH LOGIN\r\n250-AUTH=LOGIN\r\n"
#define	MSG250EHLO		"250-HELP\r\n250-EXPN\r\n250-PIPELINING\r\n250-8BITMIME\r\n250-DSN\r\n250 SIZE"

#define	MSG251NOMESSAGES	"251 OK No messages waiting for you\r\n"
#define	MSG251NOMESSAGES_LEN	36

#define	MSG252QUEUESTARTED	"252 OK Messages for you are being sent\r\n"
#define	MSG252QUEUESTARTED_LEN	40

#define	MSG250SENDEROK		"250 Sender OK\r\n"
#define	MSG250SENDEROK_LEN	15

#define	MSG250RECIPOK		"250 Recipient OK\r\n"
#define	MSG250RECIPOK_LEN	18

#define	MSG354DATA		"354 Send message, end with <CRLF>.<CRLF>\r\n"
#define	MSG354DATA_LEN		42

#define	MSG421SHUTDOWN		"Service not available: server shutting down"

#define	MSG451INTERNALERR	"451 Internal error: Unable to complete, please try later\r\n"
#define	MSG451INTERNALERR_LEN 58

#define	MSG451RECEIVERDOWN	"451 SMTP Receiver temporarily shut down, please try in a few minutes\r\n"
#define	MSG451RECEIVERDOWN_LEN 70

#define	MSG451NOCONNECTIONS	"451 Too many connections, please try later\r\n"
#define	MSG451NOCONNECTIONS_LEN 44

#define	MSG452NOSPACE		"452 Not enough disk space. Please try later\r\n"
#define	MSG452NOSPACE_LEN	45

#define MSG453TRYLATER      "453 Please try later\r\n"
#define MSG453TRYLATER_LEN  22

#define MSG553COMMENT       "553 "
#define MSG553COMMENT_LEN   4

#define MSG453COMMENT       "453 "
#define MSG453COMMENT_LEN   4

#define	MSG550SPAMBLOCK		"553 Your site is blocked due to previous spamming incidents\r\n"
#define	MSG550SPAMBLOCK_LEN	61

#define	MSG554SPAMBLOCK		"554 Your site is blocked due to apparent connection flooding\r\n"
#define	MSG554SPAMBLOCK_LEN	62

#define	MSG551SPAMBLOCK		"553 Your computer does not have a hostname, access blocked\r\n"
#define	MSG551SPAMBLOCK_LEN	60

#define	MSG552SPAMBLOCK		"553 Your IP address %d.%d.%d.%d is blackholed by %s. "

#define	MSG553SPAMBLOCK		"553 Your computer does not have a hostname, you must AUTHenticate\r\n"
#define	MSG553SPAMBLOCK_LEN	67


#define	MSG500UNKNOWN		"500 Command unrecognized\r\n"
#define	MSG500UNKNOWN_LEN	26

#define	MSG501PARAMERROR	"501 Parameter syntax error or not supported\r\n"
#define	MSG501PARAMERROR_LEN	45

#define	MSG502NOTIMPLEMENTED	"502 Command not implemented\r\n"
#define	MSG502NOTIMPLEMENTED_LEN	29

#define	MSG500TOOLONG		"500 Line too long, closing channel\r\n"
#define	MSG500TOOLONG_LEN	36

#define	MSG500NOMEMORY		"500 Out of memory, closing channel\r\n"
#define	MSG500NOMEMORY_LEN	36

#define	MSG501RECIPNO		"501 Recipient address unknown format\r\n"
#define	MSG501RECIPNO_LEN	38

#define	MSG501GOAWAY		"501 You don't exist. Go away!\r\n"
#define	MSG501GOAWAY_LEN	31

#define	MSG501NOSENDER		"501 Syntax error, no sender\r\n"
#define	MSG501NOSENDER_LEN	29

#define	MSG501SYNTAX		"501 Syntax error\r\n"
#define	MSG501SYNTAX_LEN	18

#define	MSG502DISABLED		"502 Command disabled\r\n"
#define	MSG502DISABLED_LEN	22

#define	MSG503BADORDER		"503 Invalid sequence of commands\r\n"
#define	MSG503BADORDER_LEN	34

#define	MSG504BADAUTH		"504 Unrecognized authentication type\r\n"
#define	MSG504BADAUTH_LEN	38

#define	MSG505MISSINGAUTH	"505 Authentication required\r\n"
#define	MSG505MISSINGAUTH_LEN	29

#define	MSG550NOTFOUND		"550 Mailbox not found\r\n"
#define	MSG550NOTFOUND_LEN	23

#define	MSG550TOOMANY		"550 Too many recipients\r\n"
#define	MSG550TOOMANY_LEN	25

#define	MSG552MSGTOOBIG		"552 Message size exceeds maximum allowed by this server\r\n"
#define	MSG552MSGTOOBIG_LEN	57

#define	MSG571NOROUTING		"571 No external routing allowed\r\n"
#define	MSG571NOROUTING_LEN	33

#define	MSG571SPAMBLOCK		"571 Remote sending only allowed with authentication!\r\n"
#define	MSG571SPAMBLOCK_LEN	54

#define	MSG572SPAMBOUNCEBLOCK	"572 Bounces are blocked due to system abuse by spammers\r\n"
#define	MSG572SPAMBOUNCEBLOCK_LEN	57


/* DO NOT ADD A CR/LF AT THE END OF THE FOLLOWING STRING - this is not a response code, but a fake error code for sending */
#define	MSG422CONNERROR		"422 Remote system unexpectedly closed the connection."
#define	MSG422CONNERROR_LEN	53

