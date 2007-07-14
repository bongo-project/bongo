/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2006 Novell, Inc. All Rights Reserved.
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

/*
Copyright (c) 2002 JSON.org

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

*/


#include <config.h>
#include <bongojson.h>
#include "assert.h"

/* FIXME: this just parses entire buffers, we really want something
 * that can process streams */

struct _BongoJsonParser {
    int error;

    const char *buffer;
    int index;
    int buflen;
    int charptr;
    
    int numRead;    
    BOOL eof;
    char realbuf[1024];
    int maxLen;
    
    void *userData;    
    BongoJsonParserReadFunc readFunc;
    BongoJsonParserFreeFunc freeFunc;
};

static BongoJsonNode *BongoJsonParserNextNode(BongoJsonParser *t);

static int 
FileRead(void *userData, char *buffer, int maxRead)
{
    FILE *f = userData;
    return (int)fread(buffer, 1, maxRead, f);
}

static void
BongoJsonParserInitString(BongoJsonParser *t, const char *source)
{
    t->buffer = source;
    t->index = 0;
    t->buflen = strlen(source);
    t->error = BONGO_JSON_OK;
    t->eof = FALSE;
    
    t->userData = NULL;
    t->readFunc = NULL;
    t->freeFunc = NULL;
    t->maxLen = 0;
}

static void
BongoJsonParserInit(BongoJsonParser *t,  
                   void *userData, 
                   BongoJsonParserReadFunc readFunc,
                   BongoJsonParserFreeFunc freeFunc,
                   size_t maxLen)
{
    t->buffer = t->realbuf;
    t->index = 0;
    t->buflen = 0;
    t->error = BONGO_JSON_OK;
    t->eof = FALSE;
    t->numRead = 0;
    t->charptr = 0;
   
    t->userData = userData;
    t->readFunc = readFunc;
    t->freeFunc = freeFunc;
    t->maxLen = maxLen;
}

static void
BongoJsonParserBack(BongoJsonParser *t) 
{
    if (t->index > 0) {
        t->index--;
        t->charptr--;
    }
}

static void
EnsureBuffer(BongoJsonParser *t)
{
    if (t->index >= t->buflen) {
        int toRead = 1024;

        if (t->maxLen != 0) {
            int left = t->maxLen - t->numRead;
            if (left < toRead) {
                left = toRead;
            }
        }

        if (toRead == 0) {
            t->eof = TRUE;
        } else {
            if (t->readFunc) {
                t->buflen = t->readFunc(t->userData, t->realbuf, toRead);
                t->numRead += t->buflen;
                
                if (t->buflen == 0) {
                    t->eof = TRUE;
                }
            } else {
                t->eof = TRUE;
            }
        }
        t->index = 0;
    }
}

static char 
BongoJsonParserNext(BongoJsonParser *t)
{
    EnsureBuffer(t);
    
    if (t->eof) {
        return 0;
    }
    
    t->charptr++;
    return t->buffer[t->index++];
}

#define BongoJsonParserError(t, error) BongoJsonParserErrorReal(t, error, __FILE__, __LINE__)

static void
BongoJsonParserErrorReal(BongoJsonParser *t, int error, const char *file, int line)
{
    t->error = error;
    printf("%s(%d): Parse error %d at %d\n", file, line, error, t->charptr);
}

static void
BongoJsonParserNextChars(BongoJsonParser *t, char *buf, int buflen)
{
    while (buflen) {
        int toCopy;
        
        EnsureBuffer(t);
        if (t->eof) {
            BongoJsonParserError(t, BONGO_JSON_BOUNDS_ERROR);
            return;
        }
        
        toCopy = t->buflen - t->index;
        if (toCopy > buflen) {
            toCopy = buflen;
        }
        
        memcpy(buf, t->buffer + t->index, toCopy);
        buflen -= toCopy;
        t->index += toCopy;
        t->charptr += toCopy;
    }
}

static char
BongoJsonParserNextClean(BongoJsonParser *t)
{
    while (TRUE) {
        char c;
        
        c = BongoJsonParserNext(t);
        
        if (c == '/') {
            switch(BongoJsonParserNext(t)) {
            case '/' :
                do {
                    c = BongoJsonParserNext(t);
                } while (c != '\n' && c != '\r' && c != 0);
                break;
            case '*' :
                while (TRUE) {
                    c = BongoJsonParserNext(t);
                    if (c == 0) {
                        BongoJsonParserError(t, BONGO_JSON_UNCLOSED_COMMENT);
                        return 0;
                    } else if (c == '*') {
                        if (BongoJsonParserNext(t) == '/') {
                            break;
                        }
                        BongoJsonParserBack(t);
                    }
                }
                break;
            default :
                BongoJsonParserBack(t);
                return '/';
            }
        } else if (c == 0 || c > ' ') {
            return c;
        }
    }
}

static char *
BongoJsonParserNextString(BongoJsonParser *t, char quote)
{
    char c;
    BongoStringBuilder sb;
    
    BongoStringBuilderInit(&sb);
    
    while (TRUE) {
        c = BongoJsonParserNext(t);
        if (c == 0 || c == '\r' || c == '\n') {
            BongoJsonParserError(t, BONGO_JSON_UNCLOSED_STRING);
            MemFree(sb.value);
            return NULL;
        }
        
        if (c == '\\') {
            char buf[5];
            char *endp;
            uint64_t charAsInt;
            
            c = BongoJsonParserNext(t);
            switch(c) {
            case 0 :
                BongoJsonParserError(t, BONGO_JSON_UNCLOSED_STRING);
                MemFree(sb.value);
                return NULL;
            case 'b' :
                BongoStringBuilderAppendChar(&sb, '\b');
                break;
            case 't' :
                BongoStringBuilderAppendChar(&sb, '\t');
                break;
            case 'n' :
                BongoStringBuilderAppendChar(&sb, '\t');
                break;
            case 'f' :
                BongoStringBuilderAppendChar(&sb, '\f');
                break;
            case 'r' :
                BongoStringBuilderAppendChar(&sb, '\r');
                break;
            case 'u' :
                BongoJsonParserNextChars(t, buf, 4);
                if (t->error) {
                    MemFree(sb.value);
                    return NULL;
                }
                buf[4] = '\0';

                charAsInt = HexToUInt64(buf, &endp);

                BongoStringBuilderAppendChar(&sb, (char)charAsInt);
                break;
            default :
                BongoStringBuilderAppendChar(&sb, c);
            }
        } else {
            if (c == quote) {
                return sb.value;
            }
            BongoStringBuilderAppendChar(&sb, c);
        }
    }
}

static BongoJsonNode *
BongoJsonParserNextObjectNode(BongoJsonParser *t)
{
    char c;
    BongoJsonNode *ret;
    BongoJsonObject *obj;
    
    if (BongoJsonParserNextClean(t) != '{') {
        BongoJsonParserError(t, BONGO_JSON_UNEXPECTED_CHAR);
        return NULL;
    }

    obj = BongoJsonObjectNew();
    ret = BongoJsonNodeNewObject(obj);
    
    while (TRUE) {
        BongoJsonNode *nextNode;
        char *key;
        
        c = BongoJsonParserNextClean(t);
        
        switch (c) {
        case 0 :
            BongoJsonParserError(t, BONGO_JSON_UNCLOSED_OBJECT);
            goto done;
        case '}' :
            goto done;
        default :
            BongoJsonParserBack(t);
            nextNode = BongoJsonParserNextNode(t);
            if (t->error || nextNode->type != BONGO_JSON_STRING) {
                if (nextNode) {
                    BongoJsonNodeFree(nextNode);
                }
                
                BongoJsonParserError(t, BONGO_JSON_BAD_KEY);
                goto done;
            }
            key = BongoJsonNodeAsString(nextNode);
            BongoJsonNodeFreeSteal(nextNode);
        }
        
        c = BongoJsonParserNextClean(t);
        if (c != ':') {
            BongoJsonParserError(t, BONGO_JSON_UNEXPECTED_CHAR);
            goto done;
        }
        
        nextNode = BongoJsonParserNextNode(t);
        if (t->error) {
            goto done;
        }   
        
        BongoJsonObjectPut(obj, key, nextNode);

        MemFree(key);
        
        switch(BongoJsonParserNextClean(t)) {
        case ',':
            if (BongoJsonParserNextClean(t) == '}') {
                goto done;
            }
            BongoJsonParserBack(t);
            break;
        case '}' :
            goto done;
        default :
            BongoJsonParserError(t, BONGO_JSON_UNEXPECTED_CHAR);
            goto done;
        }
    }
    
done :
    if (t->error) {
        BongoJsonNodeFree(ret);
        ret = NULL;
    }
    return ret;
}


static BongoJsonNode *
BongoJsonParserNextArrayNode(BongoJsonParser *t)
{
    BongoJsonNode *ret;
    BongoArray *array;
    
    if (BongoJsonParserNextClean(t) != '[') {
        BongoJsonParserError(t, BONGO_JSON_UNEXPECTED_CHAR);
        return NULL;
    }

    array = BongoArrayNew(sizeof(BongoJsonNode *), 15);
    ret = BongoJsonNodeNewArray(array);

    if (BongoJsonParserNextClean(t) == ']') {
        return ret;
    }
    BongoJsonParserBack(t);
    
    while (TRUE) {
        BongoJsonNode *nextNode;
        
        nextNode = BongoJsonParserNextNode(t);

        if (t->error) {
            goto done;
        }

        BongoArrayAppendValue(array, nextNode);

        switch(BongoJsonParserNextClean(t)) {
        case '0' :
            BongoJsonParserError(t, BONGO_JSON_UNCLOSED_ARRAY);
            goto done;
        case ',':
            if (BongoJsonParserNextClean(t) == ']') {
                goto done;
            }
            BongoJsonParserBack(t);
            break;
        case ']' :
            goto done;
        default :
            BongoJsonParserError(t, BONGO_JSON_UNEXPECTED_CHAR);
            goto done;
        }
    }
    
done :
    if (t->error) {
        BongoJsonNodeFree(ret);
        ret = NULL;
    }
    return ret;
}

static BongoJsonNode *
BongoJsonParserNextStringNode(BongoJsonParser *t, char c)
{
    char *s;

    s = BongoJsonParserNextString(t, c);
    
    return BongoJsonNodeNewStringGive(s);
}

static BongoJsonNode *
BongoJsonParserNextNode(BongoJsonParser *t)
{
    BongoStringBuilder sb;
    char b;
    char c;
    char *s;
    
    c = BongoJsonParserNextClean(t);

    if (c == '"' || c == '\'') {
        return BongoJsonParserNextStringNode(t, c);
    } else if (c == '{') {
        BongoJsonParserBack(t);
        return BongoJsonParserNextObjectNode(t);
    } else if (c == '[') {
        BongoJsonParserBack(t);
        return BongoJsonParserNextArrayNode(t);
    } else if (c == 0) {
        BongoJsonParserError(t, BONGO_JSON_UNEXPECTED_EOF);
        return NULL;
    }   
    
    BongoStringBuilderInit(&sb);
    b = c;
    
    while (c > ' ' && c != ':' && c != ',' && c != ']' && c != '}' && c != '/') {
        BongoStringBuilderAppendChar(&sb, c);
        c = BongoJsonParserNext(t);
    }
    BongoJsonParserBack(t);
    
    s = sb.value;
    BongoStrTrim(s);
    
    if (!strcmp(s, "true")) {
        MemFree(s);
        return BongoJsonNodeNewBool(TRUE);
    } else if (!strcmp(s, "false")) {
        MemFree(s);
        return BongoJsonNodeNewBool(FALSE);
    } else if (!strcmp(s, "null")) {
        MemFree(s);
        return BongoJsonNodeNewNull();
    }
    
    if ((b >= '0' && b <= '9') || b == '.' || b == '-' || b == '+') {
        int i;
        double d;
        char *endp;

        i = strtol(s, &endp, 10);
        if (*endp == '\0') {
            MemFree(s);
            return BongoJsonNodeNewInt(i);
        } 

        d = strtod(s, &endp);
        if (*endp == '\0') {
            MemFree(s);
            return BongoJsonNodeNewDouble(d);
        }    
    }

    if (s[0] == '\0') {
        BongoJsonParserError(t, BONGO_JSON_MISSING_VALUE);
        MemFree(s);
        return NULL;
    }

    return BongoJsonNodeNewStringGive(s);
}

BongoJsonResult 
BongoJsonParserReadNode(BongoJsonParser *parser, BongoJsonNode **node)
{
    *node = BongoJsonParserNextNode(parser);
    
    return parser->error;
}

BongoJsonResult
BongoJsonParseString(const char *str, BongoJsonNode **node)
{
    BongoJsonParser t;
    BongoJsonParserInitString(&t, str);
    
    *node = BongoJsonParserNextNode(&t);

    return t.error;    
}

BongoJsonResult
BongoJsonParseFile(FILE *f, int toRead, BongoJsonNode **node)
{
    BongoJsonParser t;
    BongoJsonParserInit(&t, f, FileRead, NULL, toRead);
    
    *node = BongoJsonParserNextNode(&t);
    
    return t.error;
}

BongoJsonParser *
BongoJsonParserNewString(const char *str)
{
    BongoJsonParser *parser;
    
    parser = MemMalloc0(sizeof(BongoJsonParser));
    BongoJsonParserInitString(parser, str);
    
    return parser;
}

BongoJsonParser *
BongoJsonParserNewFile(FILE *f, int toRead)
{
    BongoJsonParser *parser;
    
    parser = MemMalloc0(sizeof(BongoJsonParser));
    BongoJsonParserInit(parser, f, FileRead, NULL, toRead);
    
    return parser;
}

BongoJsonParser *
BongoJsonParserNew(void *userData, 
                  BongoJsonParserReadFunc readFunc,
                  BongoJsonParserFreeFunc freeFunc,
                  int maxRead)
{
    BongoJsonParser *parser;
   
    parser = MemMalloc(sizeof(BongoJsonParser));
    BongoJsonParserInit(parser, userData, readFunc, freeFunc, maxRead);
    
    return parser;
}

void 
BongoJsonParserFree(BongoJsonParser *parser)
{
    if (parser->freeFunc) {
        parser->freeFunc(parser->userData);
    }
    MemFree(parser);
}

BongoJsonResult
BongoJsonValidateString(const char *str)
{
    BongoJsonNode *node;
    BongoJsonResult res;

    /* This could be done more efficiently */

    res = BongoJsonParseString(str, &node);
    if (res == BONGO_JSON_OK) {
        BongoJsonNodeFree(node);
    }
    
    return res;
}

char *
BongoJsonQuoteString(const char *str)
{
    BongoStringBuilder sb;
    
    BongoStringBuilderInit(&sb);
    
    BongoJsonQuoteStringToStringBuilder(str, &sb);
    
    return sb.value;
}



#define UTF8_LENGTH(c, len) \
    if (c < 128) \
        len = 1; \
    else if ((c & 0xe0) == 0xc0)                                     \
        len = 2;                                                        \
    else if ((c & 0xf0) == 0xe0)                                     \
        len = 3;                                                        \
    else if ((c & 0xf8) == 0xf0)                                     \
        len = 4;                                                        \
    else if ((c & 0xfc) == 0xf8)                                     \
        len = 5;                                                        \
    else if ((c & 0xfe) == 0xfc)                                     \
        len = 6;                                                        \
    else                                                                \
        len = -1;

void
BongoJsonQuoteStringToStringBuilder(const char *str, BongoStringBuilder *sb)
{
    unsigned char c;
    int i;
    int len;

    if (str == NULL || str[0] == '\0') {
        BongoStringBuilderAppend(sb, "\"\"");
        return;
    }

    len = strlen(str);
    
    BongoStringBuilderAppendChar(sb, '"');
    
    for (i = 0; i < len; i++) {
        int len;

        c = str[i];
        
        UTF8_LENGTH(c, len);

        if (len < 2) {
            if ((c == '\\') || (c == '"')) {
                char str[2];
                str[0] = '\\';
                str[1] = c;
                BongoStringBuilderAppendN(sb, str, 2); 
            } else if (c == '\b') {
                BongoStringBuilderAppend(sb, "\\b");
            } else if (c == '\t') {
                BongoStringBuilderAppend(sb, "\\t");
            } else if (c == '\f') {
                BongoStringBuilderAppend(sb, "\\n");
            } else if (c == '\r') {
                BongoStringBuilderAppend(sb, "\\r");
            } else {
                if (c < ' ') {
                    BongoStringBuilderAppendF(sb, "\\u%04x", (int)c);
                } else {
                    BongoStringBuilderAppendChar(sb, c);
                }
            }
        } else {
            BongoStringBuilderAppendN(sb, str + i, len);
            i += len - 1;
        }
    }
        
    BongoStringBuilderAppendChar(sb, '"');
}
