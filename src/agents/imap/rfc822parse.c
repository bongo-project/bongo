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
#include "imapd.h"

/* RFC822 Address parsing */
#define BROKEN_ADDRESS_HOST  ".Syntax-Error."
#define BROKEN_ADDRESS_MAILBOX "UnexpectedDataAfterAddress"
const unsigned char *WSpecials = " ()<>@,;:\\\"[]\1\2\3\4\5\6\7\10\11\12\13\14\15\16\17\20\21\22\23\24\25\26\27\30\31\32\33\34\35\36\37\177";

#define I2C_ESC    0x1b /* ESCape */
#define I2C_MULTI    0x24 /* multi-byte character set */
#define I2CS_94_JIS_BUGROM 0x48 /* 4/8 some software does this */
#define I2CS_94_JIS_KANA 0x49 /* 4/9 JIS X 0201-1976 right half */
#define I2CS_94_JIS_ROMAN 0x4a /* 4/a JIS X 0201-1976 left half */
#define I2CS_94x94_JIS_OLD 0x40 /* 4/0 JIS X 0208-1978 */
#define I2CS_94x94_JIS_NEW 0x42 /* 4/2 JIS X 0208-1983 */
#define I2C_G0_94    0x28 /* G0 94-character set */
#define I2CS_94    0x000 /* 94 character set */
#define I2CS_94_ASCII  0x42 /* 4/2 ISO 646 USA (ASCII) */
#define I2CS_ASCII   (I2CS_94 | I2CS_94_ASCII)

static unsigned char
*RFC822SkipComment(unsigned char **String, unsigned long Trim)
{
    unsigned char *RetVal;
    unsigned char *ptr = *String;
    unsigned char *t = NULL;

    /* Skip past whitespace */
    for (RetVal = ++ptr; (*RetVal==' ' || *RetVal==0x09); RetVal++) {
        ;
    }

    do {
        switch(*ptr) {
        case '(': {
            if (!RFC822SkipComment(&ptr, 0)) {
                return(NULL);
            }
            t = --ptr;
            break;
        }

        case ')': {
            *String = ++ptr;
            if (Trim) {
                if (t) {
                    t[1] = '\0';
                } else {
                    *RetVal = '\0';
                }
            }
            return(RetVal);
        }

        case '\\': {
            if (*++ptr) {
                t = ptr;
                break;
            }
        } /* Fall-through */

        case '\0': {
            **String = '\0';
            return(NULL);
        }

        case 0x09:
        case ' ': {
            break;
        }

        default: {
            t = ptr;
            break;
        }
        }
    } while (ptr++);

    return(NULL);
}


static void
RFC822SkipWhitespace(unsigned char **String)
{
    while (TRUE) {
        if ((**String == ' ') || (**String == 0x09)) {
            ++*String;
        } else if ((**String != '(') || !RFC822SkipComment(String,0)) {
            return;
        }
    }
}


static unsigned char
*RFC822Quote(unsigned char *src)
{
    unsigned char *ret = src;

    if (strpbrk(src,"\\\"")) {
        unsigned char *dst = ret;

        while (*src) {
            if (*src == '\"') {
                src++;
            } else {
                if (*src == '\\') {
                    src++;
                }
                *dst++ = *src++;
            }
        }
        *dst = '\0';
    }
    return(ret);
}


static unsigned char
*RFC822Copy(unsigned char *Source)
{
    return(RFC822Quote(MemStrdup(Source)));
}


static unsigned char
*RFC822ParseWord(unsigned char *s, const unsigned char *Delimiters)
{
    unsigned char *st;
    unsigned char *str;

    if (!s) {
        return(NULL);
    }

    RFC822SkipWhitespace(&s);

    if (!*s) {
        return(NULL);
    }

    str = s;

    while (TRUE) {
        if (!(st = strpbrk (str, Delimiters ? Delimiters : WSpecials))) {
            return(str + strlen (str));
        }

        /* ESC in phrase */
        if (!Delimiters && (*st == I2C_ESC)) {
            str = ++st;
            switch (*st) {
            case I2C_MULTI: {
                switch (*++st) {
                case I2CS_94x94_JIS_OLD: /* old JIS (1978) */
                case I2CS_94x94_JIS_NEW: { /* new JIS (1983) */
                    str = ++st;
                    while ((st = strchr (st, I2C_ESC))!=NULL) {
                        if ((*++st == I2C_G0_94) && ((st[1] == I2CS_94_ASCII) || (st[1] == I2CS_94_JIS_ROMAN) || (st[1] == I2CS_94_JIS_BUGROM))) {
                            str = st += 2;
                            break;
                        }
                    }

                    /* eats entire text if no shift back */
                    if (!st || !*st) {
                        return(str + strlen (str));
                    }
                }
                }
                break;
            }

            case I2C_G0_94:
                switch (st[1]) {
                case I2CS_94_ASCII:
                case I2CS_94_JIS_ROMAN:
                case I2CS_94_JIS_BUGROM:
                    str = st + 2;
                    break;
                }
            }
        } else {
            switch (*st) {
            case '"': {
                while (*++st != '"') {
                    switch (*st) {
                    case '\0': {
                        return(NULL);
                    }

                    case '\\': {
                        if (!*++st) {
                            return(NULL);
                        }
                    }

                    default: {
                        break;
                    }
                    }
                }
                str = ++st;
                break;
            }

            case '\\': {
                if (st[1]) {
                    str = st + 2;
                    break;
                }
            }

            default: {
                return((st == s) ? NULL : st);
            }
            }
        }
    }
}


static unsigned char
*RFC822ParsePhrase(unsigned char *s)
{
    unsigned char *curpos;

    if (!s) {
        return(NULL);
    }

    /* find first word of phrase */
    curpos=RFC822ParseWord(s, NULL);
    if (!curpos) {
        return(NULL);
    }

    if (!*curpos) {
        return(curpos);
    }
    s = curpos;
    RFC822SkipWhitespace(&s);

    return((s = RFC822ParsePhrase(s)) ? s : curpos);
}


static RFC822AddressStruct
*RFC822ParseAddrSpec(unsigned char *Address, unsigned char **ret, unsigned char *DefaultHost)
{
    RFC822AddressStruct *Adr;
    unsigned char   c;
    unsigned char   *s;
    unsigned char   *t;
    unsigned char   *v;
    unsigned char   *end;

    if (!Address) {
        return(NULL);
    }

    RFC822SkipWhitespace(&Address);

    if (!*Address) {
        return(NULL);
    }

    if ((t=RFC822ParseWord(Address, WSpecials))==NULL) {
        return(NULL);
    }

    Adr=MemMalloc(sizeof(RFC822AddressStruct));
    memset(Adr, 0, sizeof(RFC822AddressStruct));
 
    c=*t;
    *t='\0';

    Adr->Mailbox=RFC822Copy(Address);
    *t=c;
    end=t;
    RFC822SkipWhitespace(&t);
    while (*t == '.') {
        Address = ++t;

        RFC822SkipWhitespace(&Address);

        /* get next word of mailbox */
        if ((t=RFC822ParseWord(Address, WSpecials))!=NULL) {
            end=t;
            c=*t;
            *t='\0';
            s=RFC822Copy(Address);
            *t=c;

            v = MemMalloc(strlen(Adr->Mailbox) + strlen(s) + 2);
            if (v) {
                sprintf(v, "%s.%s", Adr->Mailbox, s);
                MemFree(Adr->Mailbox);
                Adr->Mailbox = v;
            }    
            RFC822SkipWhitespace(&t);
        } else {
            break;
        }
    }

    t=end;

    RFC822SkipWhitespace(&end);

    if (*end!='@') {
        end=t;
    } else if (!(Adr->Host=RFC822ParseDomain(++end,&end))) {
        Adr->Host=MemStrdup(BROKEN_ADDRESS_HOST);
    }

    if (!Adr->Host) {
        Adr->Host=MemStrdup(DefaultHost);
    }

    if (end && !Adr->Personal) {
        while (*end==' ') {
            ++end;
        }
        if ((*end == '(') && ((s=RFC822SkipComment(&end, 1))!=NULL) && strlen (s)) {
            Adr->Personal=RFC822Copy(s);
        }
        RFC822SkipWhitespace(&end);
    }

    if (end && *end) {
        *ret=end;
    } else {
        *ret=NULL;
    }
    return(Adr);
}


unsigned char
*RFC822ParseDomain(unsigned char *Address, unsigned char **end)
{
    unsigned char *ret = NULL;
    unsigned char c;
    unsigned char *s;
    unsigned char *t;
    unsigned char *v;

    RFC822SkipWhitespace(&Address);
    if (*Address == '[') {
        if ((*end=RFC822ParseWord(Address + 1,"]\\"))!=NULL) {
            size_t len = ++*end - Address;

            ret=(unsigned char *)MemMalloc(len + 1);
            strncpy(ret, Address, len);
            ret[len]='\0';
        }
    } else if ((t=RFC822ParseWord(Address, WSpecials))!=NULL) {
        c=*t;
        *t='\0';
        ret=RFC822Copy(Address);
        *t=c;
        *end=t;
        RFC822SkipWhitespace(&t);
        while (*t=='.') {
            Address = ++t;
            RFC822SkipWhitespace(&Address);
            if ((Address=RFC822ParseDomain(Address, &t))!=NULL) {
                *end=t;
                c=*t;
                *t='\0';
                s=RFC822Copy(Address);
                *t=c;

                v=(unsigned char *)MemMalloc(strlen(ret)+strlen(s)+2);
                if (v) {
                    sprintf (v, "%s.%s", ret, s);
                    MemFree(ret);
                    ret = v;
                }
                RFC822SkipWhitespace(&t);
            } else {
                break;
            }
        }
    }

    return(ret);
}


static RFC822AddressStruct
*RFC822ParseRouteAddress(unsigned char *Address, unsigned char **ret, unsigned char *DefaultHost)
{
    RFC822AddressStruct *Adr;
    unsigned char *s;
    unsigned char *t;
    unsigned char *adl;
    size_t adllen, i;

    if (!Address) {
        return(NULL);
    }

    RFC822SkipWhitespace(&Address);

    /* must start with open bracket */
    if (*Address != '<') {
        return(NULL);
    }

    t = ++Address;

    RFC822SkipWhitespace(&t);

    for (adl = NULL, adllen = 0; (*t == '@') && ((s = RFC822ParseDomain(t + 1, &t)) != NULL); ) { /* parse possible A-D-L */
        i = strlen (s) + 2;
        if (adl) {
            unsigned char *tmp;

            tmp = MemRealloc(adl, adllen + i);
            if (tmp) {
                adl = tmp;
                sprintf (adl + adllen - 1,",@%s",s);
                adllen += i;
            }
        } else {
            adl = MemMalloc(i);
            if (adl) {
                sprintf(adl, "@%s",s);
            }
            adllen += i;
        }
  
        MemFree(s);
        RFC822SkipWhitespace(&t);
        if (*t != ',') {
            break;
        }
        t++;
        RFC822SkipWhitespace(&t);
    }

    if (adl) {
        if (*t == ':') {
            Address=++t;
        }
    }

    /* parse address spec */
    if (!(Adr=RFC822ParseAddrSpec(Address, ret, DefaultHost))) {
        if (adl) {
            MemFree(adl);
        }
        return(NULL);
    }

    if (adl) {
        Adr->ADL = adl;
    }

    if (*ret) {
        if (**ret == '>') {
            ++*ret;
            RFC822SkipWhitespace(ret);
            if (!**ret) {
                *ret=NULL;
            }
            return(Adr);
        }
    }

    /* Unterminated mailbox */
    Adr->Next=MemMalloc(sizeof(RFC822AddressStruct));
    memset(Adr->Next, 0, sizeof(RFC822AddressStruct));
    Adr->Next->Mailbox = MemStrdup(BROKEN_ADDRESS_MAILBOX);
    Adr->Next->Host=MemStrdup(BROKEN_ADDRESS_HOST);
    return(Adr);
}


static RFC822AddressStruct
*RFC822ParseMailbox(unsigned char **Address, unsigned char *DefaultHost)
{
    RFC822AddressStruct *Adr = NULL;
    unsigned char   *s;
    unsigned char   *end;

    if (!*Address) {
        return(NULL);
    }

    RFC822SkipWhitespace(Address);

    if (!**Address) {
        return(NULL);
    }

    if (*(s = *Address) == '<') {
        Adr = RFC822ParseRouteAddress(s, Address, DefaultHost);
    } else {
        if ((end = RFC822ParsePhrase(s))!=NULL) {
            if ((Adr = RFC822ParseRouteAddress(end, Address, DefaultHost))!=NULL) {
                if (Adr->Personal) {
                    MemFree(Adr->Personal);
                }
                *end = '\0';
                Adr->Personal = RFC822Copy(s);
            } else {
                Adr = RFC822ParseAddrSpec(s, Address, DefaultHost);
            }
        }
    }
    return(Adr);
}


static RFC822AddressStruct
*RFC822ParseGroup(RFC822AddressStruct **List, RFC822AddressStruct *Last, unsigned char **Address, char *DefaultHost, unsigned long Depth)
{
    unsigned char   *p;
    unsigned char   *s;
    RFC822AddressStruct *Adr;

    if (Depth > 50) {
        return(NULL);
    }
    if (!*Address) {
        return(NULL);
    }

    RFC822SkipWhitespace(Address);

    if (!**Address || ((*(p = *Address) != ':') && !(p = RFC822ParsePhrase(*Address)))) {
        return(NULL);
    }

    s = p;
    RFC822SkipWhitespace(&s);

    if (*s != ':') {
        return NULL;
    }

    *p = '\0';
    p = ++s;

    RFC822SkipWhitespace(&p);

    /* write as address */
    Adr=MemMalloc(sizeof(RFC822AddressStruct));
    memset(Adr, 0, sizeof(RFC822AddressStruct));
    Adr->Mailbox=RFC822Copy(*Address);

    if (!*List) {
        *List = Adr;
    } else {
        Last->Next = Adr;
    }

    Last = Adr;
    *Address = p;

    while (*Address && **Address && (**Address != ';')) {
        if ((Adr=RFC822ParseAddress(List, Last, Address, DefaultHost, Depth+1))!=NULL) {
            Last=Adr;
            if (*Address) {
                RFC822SkipWhitespace(Address);
                switch (**Address) {
                case ',': {
                    ++*Address;
                }

                case ';':
                case '\0': {
                    break;
                }

                default: {
                    *Address = NULL;
                    Last->Next=MemMalloc(sizeof(RFC822AddressStruct));
                    Last=Last->Next;
                    memset(Last, 0, sizeof(RFC822AddressStruct));
                    Last->Mailbox=MemStrdup(BROKEN_ADDRESS_MAILBOX);
                    Last->Host=MemStrdup(BROKEN_ADDRESS_HOST);
                }
                }
            }
        } else {
            *Address = NULL;
            Adr=MemMalloc(sizeof(RFC822AddressStruct));
            memset(Adr, 0, sizeof(RFC822AddressStruct));
            Adr->Mailbox=MemStrdup(BROKEN_ADDRESS_MAILBOX);
            Adr->Host=MemStrdup(BROKEN_ADDRESS_HOST);
            Last = Last->Next = Adr;
        }
    }

    if (*Address) {
        if (**Address == ';') {
            ++*Address;
        }
        RFC822SkipWhitespace(Address);
    }

    Adr=MemMalloc(sizeof(RFC822AddressStruct));
    memset(Adr, 0, sizeof(RFC822AddressStruct));
    Last->Next=Adr;
    Last=Adr;
    return(Last);
}


RFC822AddressStruct
*RFC822ParseAddress(RFC822AddressStruct **List, RFC822AddressStruct *Last, unsigned char **Address, unsigned char *DefaultHost, unsigned long Depth)
{
    RFC822AddressStruct *Adr;

    if (!*Address) {
        return(NULL);
    }

    RFC822SkipWhitespace(Address);

    if (!**Address) {
        return(NULL);
    }

    if ((Adr=RFC822ParseGroup(List, Last, Address, DefaultHost, Depth))!=NULL) {
        Last=Adr;
    } else if ((Adr=RFC822ParseMailbox(Address, DefaultHost))!=NULL) {
        if (!*List) {
            *List=Adr;
        } else {
            Last->Next=Adr;
        }
        for (Last=Adr; Last->Next; Last=Last->Next) {
            ;
        }
    } else if (*Address) {
        return(NULL);
    }
    return(Last);
}


BOOL
RFC822ParseAddressList(RFC822AddressStruct **List, unsigned char *Address, unsigned char *DefaultHost)
{
    RFC822AddressStruct *Last=*List;
    RFC822AddressStruct *Adr;
    unsigned char   c;
 
    if (!Address) {
        return(FALSE);
    }

    RFC822SkipWhitespace(&Address);

    if (Address[0]=='\0') {
        return(FALSE);
    }

    while (Address) {
        if ((Adr=RFC822ParseAddress(List, Last, &Address, DefaultHost, 0))!=NULL) {
            Last=Adr;
            if (Address) {
                RFC822SkipWhitespace(&Address);
                switch(c=Address[0]) {
                case ',': {
                    ++Address;
                    break;
                }

                default: {
                    Last->Next=MemMalloc(sizeof(RFC822AddressStruct));
                    Last=Last->Next;
                    memset(Last, 0, sizeof(RFC822AddressStruct));
                    Last->Mailbox=MemStrdup(BROKEN_ADDRESS_MAILBOX);
                    Last->Host=MemStrdup(BROKEN_ADDRESS_HOST);
                } /* Fall-through */

                case '\0': {
                    Address=NULL;
                    break;
                }
                }
            }
        } else if (Address) {
            RFC822SkipWhitespace(&Address);
            Address=NULL;
            Adr=MemMalloc(sizeof(RFC822AddressStruct));
            memset(Adr, 0, sizeof(RFC822AddressStruct));
            Adr->Mailbox=MemStrdup(BROKEN_ADDRESS_MAILBOX);
            Adr->Host=MemStrdup(BROKEN_ADDRESS_HOST);
            if (Last) {
                Last=Last->Next=Adr;
            } else {
                *List=Last=Adr;
            }
            break;
        }
    }
    return(TRUE);
}

void
RFC822FreeAddressList(RFC822AddressStruct *List)
{
    RFC822AddressStruct *ToFree;

    while (List) {
        if (List->Personal) {
            MemFree(List->Personal);
        }
        if (List->Mailbox) {
            MemFree(List->Mailbox);
        }
        if (List->Host) {
            MemFree(List->Host);
        }
        if (List->ADL) {
            MemFree(List->ADL);
        }
        ToFree=List;
        List=List->Next;
        MemFree(ToFree);
    }
}
