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
 * (C) 2007 Patrick Felt
 ****************************************************************************/

#ifndef MSGADDR_H
#define MSGADDR_H

#define MSGADDR_COMMENT_START   '('
#define MSGADDR_COMMENT_END     ')'

#define MSGADDR_TEXT            (1 << 0)
#define MSGADDR_ATEXT           (1 << 1)
#define MSGADDR_QTEXT           (1 << 2)
#define MSGADDR_DTEXT           (1 << 3)
#define MSGADDR_CTEXT           (1 << 4)
#define MSGADDR_XCHAR           (1 << 5)
#define MSGADDR_HEXCHAR         (1 << 6)

/*  RFC 2234 - 6.1 Core Rules
               SP     0x20      ; space
               HTAB   0x09      ; horizontal tab
               CR     0x0D      ; carriage return
               LF     0x0A      ; line feed

               WSP    SP / HTAB ; white space
               CRLF   CR LF     ; carriage return line feed

    Returns non-zero if 'c' is an RFC2234 WSP character; zero if not true.
*/
#define MSG_IS_WSP(c)     (((c) == 0x20) || ((c) == 0x09))

/*  Returns non-zero if 'p' dereferences to RFC2234 CRLF characters; zero if not true.
*/
#define MSG_IS_CRLF(p)    ((((p))[0] == 0x0D) && (((p))[1] == 0x0A))

/*  RFC 2822 - 3.2.1 Primitive Tokens
    text       %d1-9 / %d11 / %d12 / %d14-31 / %d127

    Returns non-zero if 'c' is a RFC2822 text character; zero if not true.
*/
#define MSG_IS_TEXT(c)    (((MsgAddressCharacters[(int)(unsigned char)(c)]) & MSGADDR_TEXT) != 0)

/*  RFC 2822 - 3.2.4 Atom
    atext      ALPHA / DIGIT / "!" / "#" / "$" / "%" / "&" / "'" / "*" / "+" / 
               "-" / "/" / "=" / "?" / "^" / "_" / "`" / "{" / "|" / "}" / "~"

    Returns non-zero if 'c' is an RFC2822 atext character; zero if not true.
*/
#define MSG_IS_ATEXT(c)   (((MsgAddressCharacters[(int)(unsigned char)(c)]) & MSGADDR_ATEXT) != 0)

/*  RFC 2822 - 3.2.5 Quoted strings
    qtext      NO-WS-CTL / %d33 / %d35-91 / %d93-126

    Returns non-zero if 'c' is an RFC2822 qtext character; zero if not true.
*/
#define MSG_IS_QTEXT(c)   (((MsgAddressCharacters[(int)(unsigned char)(c)]) & MSGADDR_QTEXT) != 0)

/*  RFC 2822 - 3.4.1 Addr-spec specification
    dtext      NO-WS-CTL / %d33-90 / %d94-126

    Returns non-zero if 'c' is an RFC2822 dtext character; zero if not true.
*/
#define MSG_IS_DTEXT(c)   (((MsgAddressCharacters[(int)(unsigned char)(c)]) & MSGADDR_DTEXT) != 0)

/*  RFC 2822 - 3.2.3 Folding white space and comments
    ctext      NO-WS-CTL / %d33-39 / %d42-91 / %d93-126

    Returns non-zero if 'c' is an RFC2822 ctext character; zero if not true.
*/
#define MSG_IS_CTEXT(c)   (((MsgAddressCharacters[(int)(unsigned char)(c)]) & MSGADDR_CTEXT) != 0)

/*  RFC 1891 - 3.2.5 Additional parameters for RCPT and MAIL commands
    xchar      any ASCII CHAR between "!" (33) and "~" (126) inclusive, except for "+" and "="
               (translation) %d33-42 / %d44-60 / %d62 - 126

    Returns non-zero if 'c' is an RFC1891 xchar character; zero if not true.
*/
#define MSG_IS_XCHAR(c)   (((MsgAddressCharacters[(int)(unsigned char)(c)]) & MSGADDR_XCHAR) != 0)

/* RFC 3461    - 4. Additional parameters for RCPT and MAIL commands
   hexchar = ASCII "+" immediately followed by two upper case
                hexadecimal digits    

    Returns non-zero if 'c' is an RFC3461 hexadecimal digit; zero if not true.
*/
#define MSG_IS_HEXCHAR(c) (((MsgAddressCharacters[(int)(unsigned char)(c)]) & MSGADDR_HEXCHAR) != 0)

#if defined(WIN32)

#if defined(MSGADDR_C)

EXPORT const unsigned char MsgAddressCharacters[];

EXPORT BOOL MsgParseAddress(unsigned char *AddressLine, size_t AddressLineLength, unsigned char **local_part, unsigned char **domain);
EXPORT int MsgIsComment(unsigned char *Base, unsigned char *Limit, unsigned char **Out);
EXPORT BOOL MsgIsXText(unsigned char *In, unsigned char *Limit, unsigned char **Out);

#else   /*  defined(MSGADDR_C)  */

IMPORT const unsigned char MsgAddressCharacters[];

IMPORT BOOL MsgIsAddress(unsigned char *AddressLine, size_t AddressLineLength, unsigned char *Delimiters, unsigned char **LineOut);
IMPORT int MsgIsComment(unsigned char *Base, unsigned char *Limit, unsigned char **Out);
IMPORT BOOL MsgIsXText(unsigned char *In, unsigned char *Limit, unsigned char **Out);

#endif  /*  defined(MSGADDR_C)  */

#else   /*  defined(WIN32)  */

extern const unsigned char MsgAddressCharacters[];

BOOL MsgParseAddress(unsigned char *AddressLine, size_t AddressLineLength, unsigned char **local_part, unsigned char **domain);
//BOOL MsgIsAddress(unsigned char *AddressLine, size_t AddressLineLength, unsigned char *Delimiters, unsigned char **LineOut);
int MsgIsComment(unsigned char *Base, unsigned char *Limit, unsigned char **Out);
BOOL MsgIsXText(unsigned char *In, unsigned char *Limit, unsigned char **Out);

#endif

#endif  /*  MSGADDR_H   */
