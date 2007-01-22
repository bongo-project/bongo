/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2005, 2006 Novell, Inc. All Rights Reserved.
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

/* Routines for parsing and manipulating mail messages */

#include <config.h>

#include <xpl.h>
#include <memmgr.h>
#include <logger.h>
#include <bongoutil.h>
#include <bongoagent.h>
#include <mdb.h>
#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include <connio.h>
#include <bongoutil.h>
#include <bongostream.h>

#include "mail-parser.h"
#include "mime.h"

typedef struct {
    MailHeaderName name;
    union {
        struct {
            char *email;
            char *name;
        };
        char *content_type;
        uint64_t date;        
    };
} MailHeader;

/* FIXME This is a third-assed mail header parser. */

struct MailParser {
    FILE *stream;
    MailParserParticipantsCallback participantscb;
    MailParserUnstructuredCallback unstructuredcb;
    MailParserDateCallback datecb;
    MailParserMessageIdCallback messageidcb;
    MailParserOffsetCallback offsetcb;
    void *cbarg;

    char line[2048];
    char headerName[128];
    char *p;
};


#define NEXTLINE(_retval)                                \
    if (!fgets(parser->line, 2048, parser->stream)) {    \
        return _retval;                                  \
    }                                                    \
    parser->p = parser->line;




/* requires: parser->p points to the first char following 
             the comment's opening '(' 

   returns:  -1 on EOF/error, o/w 0

   modifies: parser->p will be advanced to the next char 
             following the closing ')'
 */

static int
ParseComment(MailParser *parser)
{
    int c;

    while (1) {
        switch (*parser->p++) {
        case 0:
            NEXTLINE(-1);
            continue;
        case '(':
            if (ParseComment(parser)) {
                return -1;
            }
            continue;
        case ')':
            return 0;
        case '\\': /* quoted pair */
            c = *parser->p++;
            if (0 == c) {
                NEXTLINE(-1);
            }
            continue;
        default:
            continue;
        }
    }
}


/* returns:  0    - EOF or run-on comment
             '\n' - non-folded CRLF
             char - next non-whitespace char

   modifies: parser->line may be refreshed to the next line
             parser->p will be advanced to the next non-whitespace character
 */

static char
NextNonCFWS(MailParser *parser)
{
    int c;
#if 0
    if (parser->p == parser->line) {
        if ('\r' == *parser->p || '\n' == *parser->p) {
            /* we are at the end of the headers */
            return *parser->p;
        }
    }
#endif
    while (1) {
        switch (*parser->p++) {
        case 0:
            NEXTLINE(0);
            continue;
        case ' ':
        case '\t':
            continue;
        case '\r':
            c = *parser->p++;
            if (0 == c) {
                NEXTLINE(0);
                c = *parser->p++;
            }
            if ('\n' == c) {
        case '\n':
                NEXTLINE(0);
                if (' ' != *parser->p && '\t' != *parser->p) {
                    return '\n'; /* CRLF */
                }
                continue;
            } else {
                /* again, I think this is illegal, treat as space */
                continue;
            }
        case '(':
            if (ParseComment(parser)) {
                return 0;
            }
            continue;
        default:
            return *--parser->p;
        }
    }
}


/* requires: <buf> points to a string of up to 80 characters to be examined to 
             see if they are encoded text.  <len> specifies the capacity of <buf> 
             *<prevtext> points to the first character following the last string
             successfully decoded by this function, or NULL if the last string 
             was not encoded.
             
   modifies: if <buf> contains rfc2047-encoded text, then it will be decoded 
             in place into its UTF-8 representation, and *<prevtext> will be 
             set to the first character following the decoded string.

   returns:  the length of the decoded token minus (buf - *prevtext)
 */

static int
DecodeWord(char *buf, int len, char **prevtext)
{
    char token[81]; /* max encoded word length is 75 characters, we accept up to 80 */
    char charset[81];
    const char *encoding = NULL;
    char *pbuf = *prevtext ? *prevtext : buf;

    BongoStream *stream = NULL;

    char *t; /* input ptr to <token> */
    char *d; /* output ptr to buf */
    int tlen;
    int elen;

    tlen = strlen(buf);
    assert(tlen < 80);

    strncpy(token, buf, 80);
    *prevtext = NULL;

    /* encoded-word = "=?" charset "?" encoding "?" encoded-text "?=" */

    if ('=' != token[0] || '?' != token[1]) {
        return tlen;
    }

    /* find charset: */

    t = token + 2;
    d = charset;

    while (*t) {
        if (!*t) {
            return tlen;
        } else if ('?' == *t) {
            *d = 0;
            ++t;
            break;
        } else {
            *d++ = *t;
        }
        t++;
    }

    /* find encoding: */

    if ('B' == toupper(*t)) {
        encoding = "BASE64";
    } else if ('Q' == toupper(*t)) {
        encoding = "Q";
    } else {
        return tlen;
    }

    if ('?' != *++t) {
        return tlen;
    }

    /* verify encoded-text: */

    d = ++t;
    elen = 0;
    while (1) {
        if (0 == *d) {
            return tlen;
        } else if (' ' == *d) {
            return tlen;
        } else if ('?' == *d) {
            if ('=' != d[1] || 0 != d[2]) {
                return tlen;
            }
            break;
        }
        ++elen;
        ++d;
    }

    /* attempt to decode */

    stream = BongoStreamCreate(NULL, 0, FALSE);
    if (!stream) {
        return tlen;
    }

    if (BongoStreamAddCodec(stream, encoding, FALSE)) {
        printf ("Don't understand encoding %s\n", encoding);
        goto done;
    }

    if (BongoStreamAddCodec(stream, charset, FALSE)) {
        printf("Don't understand charset %s\n", charset);
        goto done;
    }

    if (BongoStreamAddCodec(stream, "UTF-8", TRUE)) {
        printf("Couldn't add UTF-8 encoder\n");
        goto done;
    }

    BongoStreamPut(stream, t, elen);
    tlen = BongoStreamGet(stream, pbuf, len);
    *prevtext = pbuf + tlen;
    tlen -= (buf - pbuf);

done:
    if (stream) {
        BongoStreamFree(stream);
    }

    return tlen;
}


static int
ParseUnstructured(MailParser *parser, char *buf, int len)
{
    int i = 0;
    int c;
    char *prevtext = NULL;
    int tokstart = 0;

    while (1) {
        c = *parser->p++;

        switch (c) {
        case 0:
            if (!fgets(parser->line, 2048, parser->stream)) {
                goto done;
            }
            parser->p = parser->line;
            continue;
        case '\r':
            c = *parser->p++;
            /* fall through */
        case '\n':
            if (i < len) {
                if (i > tokstart && '=' == buf[tokstart] && i - tokstart < 80) {
                    buf[i] = 0;
                    i = tokstart + 
                        DecodeWord(buf + tokstart, len - tokstart, &prevtext);
                }
            }

            if ('\n' == c) {
                if (!fgets(parser->line, 2048, parser->stream)) {
                    goto done;
                }
                parser->p = parser->line;

                if (' ' != *parser->p && '\t' != *parser->p) {
                    goto done;
                }
                if (i < len) {
                    buf[i++] = *parser->p++;
                }
                tokstart = i;
            } else {
                /* again, I think this is illegal, treat as space */
                if (i < len) {
                    buf[i++] = ' ';
                    tokstart = i;
                    if (i < len) {
                        buf[i++] = c;
                    }
                }
            }
            break;
        case ' ':
        case '\t':
            if (i < len) {
                if (i > tokstart && '=' == buf[tokstart] && i - tokstart < 80) {
                    buf[i] = 0;
                    i = tokstart + 
                        DecodeWord(buf + tokstart, len - tokstart, &prevtext);
                }
                buf[i++] = c;
            }
            tokstart = i;
            break;
            
        default:
            if (i < len) {
                buf[i++] = c;
            }
        }
    }

done:
    buf[i < len ? i++ : len - 1] = 0;
    
    return i;
}


static int
ParseQuotedString(MailParser *parser, char *buf, int len)
{
    /*
       qtext           =       NO-WS-CTL /     ; Non white space controls
      
                               %d33 /          ; The rest of the US-ASCII
                               %d35-91 /       ;  characters not including "\"
                               %d93-126        ;  or the quote character

       qcontent        =       qtext / quoted-pair

       quoted-string   =       [CFWS]
                               DQUOTE *([FWS] qcontent) [FWS] DQUOTE
                               [CFWS]
    */

    int c;
    int i = 0;

    if ('"' != *parser->p++) {
        return -1;
    }

    while (1) {
        c = *parser->p++;

        switch (c) {
        case 0:
            NEXTLINE(-1);
            continue;
        case '\r':
            c = *parser->p++;
            if ('\n' == c) {
        case '\n':
                NEXTLINE(-1);
                if (' ' != *parser->p && '\t' != *parser->p) {
                    return -1;
                }
                c = NextNonCFWS(parser);
                if ('\n' == c || 0 == c) {
                    return -1;
                }
                if (i < len) {
                    buf[i++] = ' ';
                }
            } else {
                if (i < len) {
                    buf[i++] = ' ';
                    if (i < len) {
                        buf[i++] = c;
                    }
                }
            }
            break;
        case '"':
            goto done;
        case '\\':
            c = *parser->p++;
            if (0 == c) {
                NEXTLINE(-1);
                c = *parser->p++;
            }
            /* fall through: */
        default:
            if (i < len) {
                buf[i++] = c;
            }
        }
    }

done:
    buf[i < len ? i++ : len - 1] = 0;

    return i;
}


/* returns: approximate number of tokens in phrase, -1 on error */

static int
ParsePhrase(MailParser *parser, char *buf, int len)
{
    int c;
    int i = 0; 
    int tokens = 0;
    int n;
    int tokstart;
    char *prevtext = NULL;

    --len;

loop:
    tokstart = i;

    c = NextNonCFWS(parser);
    if ('\n' == c || 0 == c) {
        goto done;
    } else {
        tokens++;
        while (1) {
            c = *parser->p;
            switch (c) {
            case 0:
                if (!fgets(parser->line, 2048, parser->stream)) {
                    goto done;
                }
                parser->p = parser->line;
                continue;
            case '"':
                if (-1 == (n = ParseQuotedString(parser, buf + i, len - i))) {
                    return -1;
                }
                i += n;
                continue;
            case '<':
            case '>':
            case '@':
            case ':':
            case ';':
            case ',':
                goto done;
            case ' ':
            case '\t':
            case '\r':
                if (i < len) {
                    if ('=' == buf[tokstart] && i - tokstart < 80) {
                        buf[i] = 0;
                        i = tokstart + 
                            DecodeWord(buf + tokstart, len - tokstart, &prevtext);
                    }
                    buf[i++] = ' ';
                }
                goto loop;
            case '\n':
                if (i < len) {
                    buf[i++] = ' ';
                }
                goto done;
            case '(':
                parser->p++;
                if (ParseComment(parser)) {
                    goto done;
                }
                continue;
            default:
                if (i < len) {
                    buf[i++] = c;
                }
            }
            ++parser->p;
        } 
    }
done:
    if (i && ' ' == buf[i-1]) {
        buf[i-1] = 0;
    } else {
        buf[i] = 0;
    }
    return tokens;
}


static int
ParseDotAtom(MailParser *parser, char *buf, int len)
{
    /* bad */
    if (0 == ParsePhrase(parser, buf, len)) {
        return -1;
    }
    return strlen(buf);
}

static int
ParseLocalPart(MailParser *parser, char *buf, int len)
{
    return ParseDotAtom(parser, buf, len);
}


static int
ParseDomain(MailParser *parser, char *buf, int len)
{
    return ParseDotAtom(parser, buf, len);
}

static int
ParseAngleAddr(MailParser *parser, char *buf, int len)
{
    int cnt;
    int pcode;

    ++parser->p;

    cnt = pcode = ParseLocalPart(parser, buf, len - 1);
    if (-1 == pcode) {
        return 0;
    }
    if ('@' != NextNonCFWS(parser)) {
        return 0;
    }
    ++parser->p;
    buf[cnt++] = '@';
    cnt += pcode = ParseDomain(parser, buf + cnt, len - cnt);
    if (-1 == pcode) {
        return 0;
    }

    if ('>' != NextNonCFWS(parser)) {
        return 0;
    }
    ++parser->p;

    return cnt;    
}


static int ParseAddress(MailParser *parser, MailHeaderName header);
static int ParseAddressList(MailParser *parser, MailHeaderName header);


static int
ParseMailbox(MailParser *parser, MailHeaderName header)
{
    /* FIXME */
    return ParseAddress(parser, header);
}


static int
ParseMailboxList(MailParser *parser, MailHeaderName header)
{
    /* 1*([mailbox] [CFWS] "," [CFWS]) [mailbox] */

    /* FIXME */
    return ParseAddressList(parser, header);
}


/* returns: 1  - address found
            0  - no address found
            -1 - error
*/       


static int
ParseAddress(MailParser *parser,
             MailHeaderName header)
{
    /* mailbox / group */
    
    char c;
    MailAddress addr;
    char phrase[1024];

    c = NextNonCFWS(parser);

    if ('\n' == c || ';' == c || 0 == c) {
        return 0;
    } else if (',' == c) {
        /* forgive empty addresses in addresslists. */
        return 0;
    } else if ('<' == c) {
        if (-1 == ParseAngleAddr(parser, phrase, sizeof(phrase))) {
            /* Sometimes the address has the illegal form "BLAH <>" */
            phrase[0] = 0;
        }
        addr.displayName = NULL;
        addr.address = phrase;
        if (parser->participantscb) {
            parser->participantscb(parser->cbarg, header, &addr);
        }
        
        return 1;
    } else {
        
        if (0 == ParsePhrase(parser, phrase, sizeof(phrase))) {
            return -1;
        }

        c = NextNonCFWS(parser);
        if (':' == c) {
            /* we have a group */
            ++parser->p;
            
            if (-1 == ParseMailboxList(parser, header)) {
                return -1;
            }
            c = NextNonCFWS(parser);
            if (';' == c) {
                ++parser->p;
                return 0;
            }
            return -1;
        } else if ('@' == c) {
            int len;
            addr.displayName = NULL;
            len = strlen(phrase);
            phrase[len++] = '@';
            ++parser->p;
            if (-1 == ParseDomain(parser, phrase + len, sizeof(phrase) - len)) {
                return -1;
            }
            addr.address = phrase;
            if (parser->participantscb) {
                parser->participantscb(parser->cbarg, header, &addr);           
            }
            
            return 1;
        } else if ('<' == c) {
            char tmp[1024];
            memcpy(tmp, phrase, sizeof(tmp));
            addr.displayName = tmp;
            if (-1 == ParseAngleAddr(parser, phrase, sizeof(phrase))) {
                return -1;
            }
            addr.address = phrase;
            if (parser->participantscb) {
                parser->participantscb(parser->cbarg, header, &addr);
            }
            
            return 1;
        } else {
            return -1;
        }
    }
}


static int 
ParseAddressList(MailParser *parser, 
                 MailHeaderName header)
{
    /* (address *("," address)) */
    int pcode;

    pcode = ParseAddress(parser, header);
    if (-1 == pcode) {
        return -1;
    }
    while (1) {
        char c = NextNonCFWS(parser);
        if (',' == c) {
            ++parser->p;
            return ParseAddressList(parser, header);
        } else {
            return 0;
        }
    }
}

static int
ParseUnstructuredHeader(MailParser *parser, MailHeaderName header)
{
    char buffer[2048];
    int c;

    c = NextNonCFWS(parser);

    if (0 == c || '\n' == c) {
        return 0;
    }
    if (-1 == ParseUnstructured(parser, buffer, sizeof(buffer))) {
        return -1;
    }

    if (parser->unstructuredcb) {
        parser->unstructuredcb(parser->cbarg, header, parser->headerName, buffer);
    }
    return 1;
}

static int
ParseDateHeader(MailParser *parser, MailHeaderName header)
{
    char buffer[2048];
    int c;
    uint64_t date;

    c = NextNonCFWS(parser);

    if (':' == c) { 
        /* forgive Groupwise spam filter messages */
        ++parser->p;
        c = NextNonCFWS(parser);
    }


    if (0 == c || '\n' == c) {
        return 0;
    }
    
    if (-1 == ParseUnstructured(parser, buffer, sizeof(buffer))) {
        return -1;
    }    

    date = MsgParseRFC822DateTime(buffer);
    
    if (0 == date) {
        return 0;
    }

    if (parser->datecb) {
        parser->datecb(parser->cbarg, header, date);
    }

    return 1;
}

static int 
ParseIdLeft(MailParser *parser, char *buf, int len)
{
    return ParseDotAtom(parser, buf, len);
}

static int 
ParseIdRight(MailParser *parser, char *buf, int len)
{
    return ParseDotAtom(parser, buf, len);    
}

static int
ParseAngleMessageId(MailParser *parser, char *buf, int len)
{
    int cnt;
    int pcode;

    ++parser->p;

    cnt = pcode = ParseIdLeft(parser, buf, len - 1);
    if (-1 == pcode) {
        return -1;
    }
    if ('@' != NextNonCFWS(parser)) {
        return -1;
    }
    ++parser->p;
    buf[cnt++] = '@';
    cnt += pcode = ParseIdRight(parser, buf + cnt, len - cnt);
    if (-1 == pcode) {
        return -1;
    }

    if ('>' != NextNonCFWS(parser)) {
        return -1;
    }
    ++parser->p;

    return cnt;    
}

static int
ParseMessageId(MailParser *parser,
               MailHeaderName header)
{
    /* mailbox / group */
    
    char c;
    char phrase[1024];

    c = NextNonCFWS(parser);

    if ('\n' == c || ';' == c || 0 == c) {
        return 0;
    } else if ('<' == c) {
        int length;
        
        phrase[0] = '<';
        length = ParseAngleMessageId(parser, phrase + 1, sizeof(phrase) - 1);
        if (length == -1 || (length - 1) >= (int)sizeof(phrase)) {
            return -1;
        }
        phrase[length + 1] = '>';
        phrase[length + 2] = '\0';
        
        if (parser->messageidcb) {
            parser->messageidcb(parser->cbarg, header, phrase);
        }
            
        return 1;
    }

    return 0;
}

static int
ParseInReplyTo(MailParser *parser,
               MailHeaderName header)
{
    /* Grab the first and only the first msg id */
    int ret = 0;
    char c;
    
    c = NextNonCFWS(parser);
    
    while (ret >= 0 && !('\n' == c || 0 == c)) {
        if ('<' == c && ret != 1) {
            ret = ParseMessageId(parser, header);
        } else {
            parser->p++;
            c = NextNonCFWS(parser);
        }
    }

    return ret;
}

static int 
ParseMessageIdList(MailParser *parser, 
                   MailHeaderName header)
{
    /* (msg-id *(msg-id)) */
    int pcode;

    pcode = ParseMessageId(parser, header);

    while (pcode == 1) {
        pcode = ParseMessageId(parser, header);
    }

    return pcode;
}

static int
ParseHeader(MailParser *parser, 
            MailHeaderName header)
{
    int ret;

    switch (header) {
    case MAIL_FROM:
        return ParseMailboxList(parser, header);
    case MAIL_SENDER:
    case MAIL_X_AUTH_OK:
    case MAIL_X_BEEN_THERE:
    case MAIL_X_LOOP:
        ret = ParseMailbox(parser, header);
        /* FIXME: this is awkward, and sometimes shoots us into the message body */
        NextNonCFWS(parser);
        return ret;
    case MAIL_REPLY_TO:
    case MAIL_TO:
    case MAIL_CC:
    case MAIL_BCC:
        return ParseAddressList(parser, header);
    case MAIL_DATE:
        return ParseDateHeader(parser, header);
    case MAIL_SPAM_FLAG:
    case MAIL_SUBJECT:
    case MAIL_LIST_ID:
    case MAIL_OPTIONAL:
        return ParseUnstructuredHeader(parser, header);
    case MAIL_MESSAGE_ID:
        ret = ParseMessageId(parser, header);
        NextNonCFWS(parser);
        return ret;
    case MAIL_IN_REPLY_TO:
        return ParseInReplyTo(parser, header);
    case MAIL_REFERENCES:
        return ParseMessageIdList(parser, header);
    case MAIL_OFFSET_BODY_START:
        return 1;
    }
    
    return -1;
}

int
MailParseHeaders(MailParser *parser)
{
    char *q;
    
    MailHeaderName header;

    if (!fgets(parser->line, 2048, parser->stream)) {
        return -1;
    }
    parser->p = parser->line;

    while (1) {
        if (!*parser->p) { 
           /* early EOF, this is an error */
            break;
        } else if ('\r' == *parser->p && '\n' == *(parser->p + 1)) {
            break;
        } else if ('\n' == *parser->p) {
            break;
        }

        q = strchr(parser->p, ':');
        if (!q) {
            /* we are probably at the beginning of the message body */
            break;
        }

        *q = 0;

        header = MAIL_OPTIONAL;

        switch (parser->p[0]) {
        case 'B': case 'b':
            if (!XplStrCaseCmp(parser->p, "Bcc")) {
                header = MAIL_BCC;
            }
            break;

        case 'C': case 'c':
            if (!XplStrCaseCmp(parser->p, "Cc")) {
                header = MAIL_CC;
            }
            break;

        case 'D': case 'd':
            if (!XplStrCaseCmp(parser->p, "Date")) {
                header = MAIL_DATE;
            }
            break;

        case 'F': case 'f':
            if (!XplStrCaseCmp(parser->p, "From")) {   
                header = MAIL_FROM;
            }
            break;

        case 'L': case 'l':
            if (!XplStrCaseCmp(parser->p, "List-Id")) {
                header = MAIL_LIST_ID;
            }
            break;

        case 'M': case 'm':
            if (!XplStrCaseCmp(parser->p, "Message-Id")) {
                header = MAIL_MESSAGE_ID;
            }
            break;

        case 'I': case 'i':
            if (!XplStrCaseCmp(parser->p, "In-Reply-To")) {
                header = MAIL_IN_REPLY_TO;
            }
            break;

        case 'R': case 'r':
            if (!XplStrCaseCmp(parser->p, "Reply-to")) {
                header = MAIL_REPLY_TO;
            } else if (!XplStrCaseCmp(parser->p, "References")) {
                header = MAIL_REFERENCES;
            }
            break;

        case 'S': case 's':
            if (!XplStrCaseCmp(parser->p, "Sender")) {
                header = MAIL_SENDER;
            } else if (!XplStrCaseCmp(parser->p, "Subject")) {
                BongoStrNCpy(parser->headerName, parser->p, sizeof(parser->headerName));
                header = MAIL_SUBJECT;
            }
            break;

        case 'T': case 't':
            if (!XplStrCaseCmp(parser->p, "To")) {
                header = MAIL_TO;
            }
            break;

        case 'X': case 'x':
            if (parser->p[1] != '-') {
                break;
            }

            /* there are a lot of X- headers so switch on these as well */
            switch (parser->p[2]) {
            case 'A': case 'a':
                if (!XplStrCaseCmp(parser->p, "X-Auth-OK")) {
                    header = MAIL_X_AUTH_OK;
                }
                break;
                
            case 'B': case 'b':
                if (!XplStrCaseCmp(parser->p, "X-BeenThere")) {
                    header = MAIL_X_BEEN_THERE;
                }
                break;

            case 'H': case 'h':
                if (!XplStrCaseCmp(parser->p, "X-Bongo-Ipecac")) {
                    return -1;
                }
                break;
                
            case 'L': case 'l':
                if (!XplStrCaseCmp(parser->p, "X-Loop")) {
                    header = MAIL_X_LOOP;
                }
                break;
                
            case 'S': case 's':
                if (!XplStrCaseCmp(parser->p, "X-Spam-Flag")) {
                    BongoStrNCpy(parser->headerName, parser->p, sizeof(parser->headerName));
                    header = MAIL_SPAM_FLAG;
                }
                break;
            }
            break;
        }

        if (header == MAIL_OPTIONAL) {
            BongoStrNCpy(parser->headerName, parser->p, sizeof(parser->headerName));
        }

        *q = ':';
        parser->p = q + 1;

        if (-1 == ParseHeader(parser, header)) {
            printf("didn't understand header: %d: %s", header, q + 1);
            if (parser->p != parser->line) {
                /* nevertheless, we've got to soldier on */
                NEXTLINE(0);
            }
        }
    }

    if (parser->offsetcb) {
        parser->offsetcb(parser->cbarg, 
                         MAIL_OFFSET_BODY_START, ftell(parser->stream));
    }
    return 0;
}

int
MailParseBody(MailParser *parser)
{
    return 0;
}

#if 0
static void
Participants(void *arg, 
             MailHeaderName header, 
             MailAddress *addr)
{

        fprintf (stderr, "[%s] (%s) <%s>\r\n",
                 MAIL_FROM == header ? "From" :
                 MAIL_SENDER == header ? "Sender" :
                 MAIL_REPLY_TO == header ? "Reply-to" :
                 MAIL_TO == header ? "To" :
                 MAIL_CC == header ? "Cc" :
                 MAIL_BCC == header ? "Bcc" :
                 "Unknown",
                 addr->displayName,
                 addr->address);
        
}
#endif

MailParser *
MailParserInit(FILE *stream, void *cbData)
{
    MailParser *p = MemMalloc0(sizeof(MailParser));
    
    p->stream = stream;
    p->cbarg = cbData;

    return p;
    
}

void 
MailParserDestroy(MailParser *p)
{
    MemFree(p);
}

void 
MailParserSetParticipantsCallback(MailParser *p, 
                                  MailParserParticipantsCallback cbfunc)
{
    p->participantscb = cbfunc;
}

void 
MailParserSetUnstructuredCallback(MailParser *p, 
                                  MailParserUnstructuredCallback cbfunc)
{
    p->unstructuredcb = cbfunc;
}

void 
MailParserSetDateCallback(MailParser *p, 
                          MailParserDateCallback cbfunc)
{
    p->datecb = cbfunc;
}

void 
MailParserSetOffsetCallback(MailParser *p, 
                          MailParserOffsetCallback cbfunc)
{
    p->offsetcb = cbfunc;
}

void
MailParserSetMessageIdCallback(MailParser *p,
                               MailParserMessageIdCallback cbfunc)
{
    p->messageidcb = cbfunc;
}
                               


