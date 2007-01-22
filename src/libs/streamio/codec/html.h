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

/* 
    This takes an HTML message and makes it viewable 

    URL should be set to the url of the server to use for the redirects
    Charset should be set to the charset of the user before starting the stream
*/
/* #define HTML_CODEC_DEBUG_FILE_PATH "/var/DebugCodecHtml.log" */

#if defined(HTML_CODEC_DEBUG_FILE_PATH)
#define OpenDebugLog(DEBUG_LOG_HANDLE, DEBUG_LOG_PATH)                     DEBUG_LOG_HANDLE = fopen(DEBUG_LOG_PATH, "a"); if (!DEBUG_LOG_HANDLE) {XplConsolePrintf("STREAMIO: HTML_Decode could not open %s\n", DEBUG_LOG_PATH);}
#define CloseDebugLog(DEBUG_LOG_HANDLE)                                    if (DEBUG_LOG_HANDLE) {fclose(DEBUG_LOG_HANDLE);}
#define CopyCharDebugLog(DEBUG_LOG_HANDLE, DEBUG_IN_PTR)                   if (DEBUG_LOG_HANDLE) {fprintf(DEBUG_LOG_HANDLE, "%c", **DEBUG_IN_PTR);}
#define SkipCharDebugLog(DEBUG_LOG_HANDLE, DEBUG_IN_PTR)                   if (DEBUG_LOG_HANDLE) {fprintf(DEBUG_LOG_HANDLE, "[%c]", **DEBUG_IN_PTR);}
#define InsertCharDebugLog(DEBUG_LOG_HANDLE, DEBUG_IN_CHAR)                if (DEBUG_LOG_HANDLE) {fprintf(DEBUG_LOG_HANDLE, "{%c}", DEBUG_IN_CHAR);}
#define CopyCharsDebugLog(DEBUG_LOG_HANDLE, DEBUG_SIZE, DEBUG_IN_PTR)      if (DEBUG_LOG_HANDLE) {fwrite(*DEBUG_IN_PTR, DEBUG_SIZE, 1, DEBUG_LOG_HANDLE);}
#define SkipCharsDebugLog(DEBUG_LOG_HANDLE, DEBUG_SIZE, DEBUG_IN_PTR)      if (DEBUG_LOG_HANDLE) {fwrite("[", 1, 1, DEBUG_LOG_HANDLE);fwrite(*DEBUG_IN_PTR, DEBUG_SIZE, 1, DEBUG_LOG_HANDLE);fwrite("]", 1, 1, DEBUG_LOG_HANDLE);}
#define InsertCharsDebugLog(DEBUG_LOG_HANDLE, DEBUG_SIZE, DEBUG_IN_STRING) if (DEBUG_LOG_HANDLE) {fwrite("{", 1, 1, DEBUG_LOG_HANDLE);fwrite(DEBUG_IN_STRING, DEBUG_SIZE, 1, DEBUG_LOG_HANDLE);fwrite("}", 1, 1, DEBUG_LOG_HANDLE);}
#define ChangeStateDebugLog(DEBUG_LOG_HANDLE, STATE_STRING)                if (DEBUG_LOG_HANDLE) {fprintf(DEBUG_LOG_HANDLE, "[%s]", STATE_STRING);}

#else
#define OpenDebugLog(DEBUG_LOG_HANDLE, DEBUG_LOG_PATH)
#define CloseDebugLog(DEBUG_LOG_HANDLE)
#define CopyCharDebugLog(DEBUG_LOG_HANDLE, DEBUG_IN_PTR)
#define SkipCharDebugLog(DEBUG_LOG_HANDLE, DEBUG_IN_PTR)
#define InsertCharDebugLog(DEBUG_LOG_HANDLE, DEBUG_IN_CHAR)
#define CopyCharsDebugLog(DEBUG_LOG_HANDLE, DEBUG_SIZE, DEBUG_IN_PTR)
#define SkipCharsDebugLog(DEBUG_LOG_HANDLE, DEBUG_SIZE, DEBUG_IN_PTR)
#define InsertCharsDebugLog(DEBUG_LOG_HANDLE, DEBUG_SIZE, DEBUG_IN_STRING)
#define ChangeStateDebugLog(DEBUG_LOG_HANDLE, STATE_STRING)
#endif 

#define	MINIMUM_HTML_PROCESS_SIZE	50
#define MAX_CHARSET_SIZE 50    /* if somebody sends us something bigger, we don't know what it is anyway */

/* Codec States */
#define CODEC_HTML_SEND_CONTENT                                  0
#define CODEC_HTML_SEND_TAG_EXAMINE                              1
#define CODEC_HTML_SEND_TAG                                      2
#define CODEC_HTML_SEND_TAG_SPACE                                3
#define CODEC_HTML_SEND_TAG_ATTRIBUTE_EXAMINE                    4
#define CODEC_HTML_SEND_TAG_ATTRIBUTE_NAME                       5
#define CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE                      6
#define CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE_DOUBLE_QUOTED        7
#define CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE_SINGLE_QUOTED        8
#define CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE_TEXT                 9
#define CODEC_HTML_SEND_TAG_URL_NAME                            10
#define CODEC_HTML_SEND_TAG_URL_PRE_VALUE                       11
#define CODEC_HTML_SEND_TAG_URL_VALUE                           12
#define CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED_EXAMINE     13
#define CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED_EXAMINE     14
#define CODEC_HTML_SEND_TAG_URL_VALUE_TEXT_EXAMINE              15
#define CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED             16
#define CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED_JAVASCRIPT  17
#define CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED_MAILTO      18
#define CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED             19
#define CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED_JAVASCRIPT  20
#define CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED_MAILTO      21
#define CODEC_HTML_SEND_TAG_URL_VALUE_TEXT                      22
#define CODEC_HTML_SEND_TAG_URL_VALUE_TEXT_JAVASCRIPT           23
#define CODEC_HTML_SEND_TAG_URL_VALUE_TEXT_MAILTO               24

#define CODEC_HTML_SKIP_TAG                                     25
#define CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME                      26
#define CODEC_HTML_SKIP_TAG_ATTRIBUTE_VALUE                     27
#define CODEC_HTML_SKIP_TAG_ATTRIBUTE_VALUE_DOUBLE_QUOTED       28
#define CODEC_HTML_SKIP_TAG_ATTRIBUTE_VALUE_SINGLE_QUOTED       29
#define CODEC_HTML_SKIP_TAG_ATTRIBUTE_VALUE_TEXT                30

#define CODEC_HTML_SUPRESS_CONTENT                              31
#define CODEC_HTML_SUPRESS_TAG_EXAMINE                          32
#define CODEC_HTML_SUPRESS_TAG                                  33

#define CODEC_HTML_SUPRESS_META                                 34
#define CODEC_HTML_SUPRESS_META_CHARSET                         35
#define CODEC_HTML_SUPRESS_META_CHARSET_PRE_EQUAL               36
#define CODEC_HTML_SUPRESS_META_CHARSET_PRE_VALUE               37
#define CODEC_HTML_SUPRESS_META_CHARSET_VALUE                   38

#define CODEC_HTML_SKIP_META                                    39
#define CODEC_HTML_SKIP_META_CHARSET                            40
#define CODEC_HTML_SKIP_META_CHARSET_PRE_EQUAL                  41
#define CODEC_HTML_SKIP_META_CHARSET_PRE_VALUE                  42
#define CODEC_HTML_SKIP_META_CHARSET_VALUE                      43

unsigned char *StateString[] = {
"CODEC_HTML_SEND_CONTENT",
"CODEC_HTML_SEND_TAG_EXAMINE",
"CODEC_HTML_SEND_TAG",
"CODEC_HTML_SEND_TAG_SPACE",
"CODEC_HTML_SEND_TAG_ATTRIBUTE_EXAMINE",
"CODEC_HTML_SEND_TAG_ATTRIBUTE_NAME",
"CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE",
"CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE_DOUBLE_QUOTED",
"CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE_SINGLE_QUOTED",
"CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE_TEXT",
"CODEC_HTML_SEND_TAG_URL_NAME",
"CODEC_HTML_SEND_TAG_URL_PRE_VALUE",
"CODEC_HTML_SEND_TAG_URL_VALUE",
"CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED_EXAMINE",
"CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED_EXAMINE",
"CODEC_HTML_SEND_TAG_URL_VALUE_TEXT_EXAMINE",
"CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED",
"CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED_JAVASCRIPT",
"CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED_MAILTO",
"CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED",
"CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED_JAVASCRIPT",
"CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED_MAILTO",
"CODEC_HTML_SEND_TAG_URL_VALUE_TEXT",
"CODEC_HTML_SEND_TAG_URL_VALUE_TEXT_JAVASCRIPT",
"CODEC_HTML_SEND_TAG_URL_VALUE_TEXT_MAILTO",
"CODEC_HTML_SKIP_TAG",
"CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME",
"CODEC_HTML_SKIP_TAG_ATTRIBUTE_VALUE",
"CODEC_HTML_SKIP_TAG_ATTRIBUTE_VALUE_DOUBLE_QUOTED",
"CODEC_HTML_SKIP_TAG_ATTRIBUTE_VALUE_SINGLE_QUOTED",
"CODEC_HTML_SKIP_TAG_ATTRIBUTE_VALUE_TEXT",
"CODEC_HTML_SUPRESS_CONTENT",
"CODEC_HTML_SUPRESS_TAG_EXAMINE",
"CODEC_HTML_SUPRESS_TAG",
"CODEC_HTML_SUPRESS_META",
"CODEC_HTML_SUPRESS_META_CHARSET",
"CODEC_HTML_SUPRESS_META_CHARSET_PRE_EQUAL",
"CODEC_HTML_SUPRESS_META_CHARSET_PRE_VALUE",
"CODEC_HTML_SUPRESS_META_CHARSET_VALUE",
"CODEC_HTML_SKIP_META",
"CODEC_HTML_SKIP_META_CHARSET",
"CODEC_HTML_SKIP_META_CHARSET_PRE_EQUAL",
"CODEC_HTML_SKIP_META_CHARSET_PRE_VALUE",
"CODEC_HTML_SKIP_META_CHARSET_VALUE"
};

typedef struct {
    unsigned char *name;
    unsigned long state;
} HtmlAttributeStruct;

typedef struct {
    unsigned char *name;
    unsigned long nameLen;
    unsigned long state;
} HtmlAttributeInfoStruct;

HtmlAttributeInfoStruct *HtmlAttributeInfo = NULL;

HtmlAttributeStruct HtmlAttribute[] = {
  {"href ",             CODEC_HTML_SEND_TAG_URL_NAME},
  {"href\t",            CODEC_HTML_SEND_TAG_URL_NAME},
  {"href=",             CODEC_HTML_SEND_TAG_URL_NAME},
  {"src ",              CODEC_HTML_SEND_TAG_URL_NAME},
  {"src\t",             CODEC_HTML_SEND_TAG_URL_NAME},
  {"src=",              CODEC_HTML_SEND_TAG_URL_NAME},
  {"xmlns ",            CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"xmlns\t",           CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"xmlns=",            CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onload ",           CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onload\t",          CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onload=",           CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onunload ",         CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onunload\t",        CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onunload=",         CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onclick ",          CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onclick\t",         CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onclick=",          CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"ondblclick ",       CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"ondblclick\t",      CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"ondblclick=",       CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onmousedown ",      CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onmousedown\t",     CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onmousedown=",      CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onmouseup ",        CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onmouseup\t",       CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onmouseup=",        CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onmouseover ",      CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onmouseover\t",     CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onmouseover=",      CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onmousemove ",      CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onmousemove\t",     CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onmousemove=",      CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onmouseout ",       CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onmouseout\t",      CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onmouseout=",       CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onfocus ",          CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onfocus\t",         CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onfocus=",          CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onblur ",           CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onblur\t",          CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onblur=",           CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onkeypress ",       CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onkeypress\t",      CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onkeypress=",       CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onkeydown ",        CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onkeydown\t",       CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onkeydown=",        CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onkeyup ",          CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onkeyup\t",         CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onkeyup=",          CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onsubmit ",         CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onsubmit\t",        CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onsubmit=",         CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onreset ",          CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onreset\t",         CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onreset=",          CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onselect ",         CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onselect\t",        CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onselect=",         CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onchange ",         CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onchange\t",        CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {"onchange=",         CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME},
  {NULL, 0}
};

typedef struct {
    unsigned char *name;
    unsigned char *endSupressTag;
    unsigned long sendState;       /* next state if the current mode is SEND */
    unsigned long supressState;    /* next state if the current mode is SUPPRESS */
    unsigned long endSupressState; /* next state if the current mode is SUPPRESS and end tag is being processed */
} HtmlTagsStruct;

HtmlTagsStruct HtmlTag[] = {
  {"<!",           NULL,            CODEC_HTML_SKIP_TAG,          CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"<?",           NULL,            CODEC_HTML_SKIP_TAG,          CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"<HEAD",        "</HEAD>",       CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"</HEAD>",      "</HEAD>",       CODEC_HTML_SKIP_TAG,          CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"<APPLET",      "</APPLET>",     CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"</APPLET>",    "</APPLET>",     CODEC_HTML_SKIP_TAG,          CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"<SCRIPT",      "</SCRIPT>",     CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"</SCRIPT>",    "</SCRIPT>",     CODEC_HTML_SKIP_TAG,          CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"<EMBED",       "</EMBED>",      CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"</EMBED>",     "</SCRIPT>",     CODEC_HTML_SKIP_TAG,          CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"<OBJECT",      "</OBJECT>",     CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"</OBJECT>",    "</OBJECT>",     CODEC_HTML_SKIP_TAG,          CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"<IFRAME",      "</IFRAME>",     CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"</IFRAME>",    "</IFRAME>",     CODEC_HTML_SKIP_TAG,          CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"<FRAMESET",    "</FRAMESET>",   CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"</FRAMESET>",  "</FRAMESET>",   CODEC_HTML_SKIP_TAG,          CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"<BODY",        "</BODY>",       CODEC_HTML_SKIP_TAG,          CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"</BODY>",      "</BODY>",       CODEC_HTML_SKIP_TAG,          CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"<HTML",        "</HTML>",       CODEC_HTML_SKIP_TAG,          CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"</HTML>",      "</HTML>",       CODEC_HTML_SKIP_TAG,          CODEC_HTML_SUPRESS_TAG,       CODEC_HTML_SKIP_TAG},
  {"<META",        NULL,            CODEC_HTML_SKIP_META,         CODEC_HTML_SUPRESS_META,      CODEC_HTML_SKIP_TAG},
  {NULL,           NULL,            0,                            0,                            0}
};

typedef struct {
    unsigned char *name;
    unsigned long nameLen;
    long endId;
    unsigned long sendState;
    unsigned long supressState;
    unsigned long endSupressState;
} HtmlTagsInfoStruct;

BongoKeywordIndex *HtmlAttributeIndex = NULL;
unsigned long MaxAttributeLen = 0;


BongoKeywordIndex *HtmlTagsIndex = NULL;
HtmlTagsInfoStruct *HtmlTagInfo = NULL;
unsigned long MaxTagLen = 0;

static BOOL
CodecHtmlInit(void)
{
    unsigned long i;
    unsigned long elements;

    BongoKeywordIndexCreateFromTable(HtmlAttributeIndex, HtmlAttribute, .name, TRUE);

    if (!HtmlAttributeIndex) {
        return(FALSE);
    }

    elements = 0;
    while (HtmlAttribute[elements].name != NULL) {
        elements++;
    }

    HtmlAttributeInfo = MemMalloc(sizeof(HtmlAttributeInfoStruct) * elements);
    if (!HtmlAttributeInfo) {
        BongoKeywordIndexFree(HtmlAttributeIndex);
        return(FALSE);
    }

    for (i = 0; i < elements; i++) {
        HtmlAttributeInfo[i].name = HtmlAttribute[i].name;
        HtmlAttributeInfo[i].nameLen = strlen(HtmlAttribute[i].name);
        MaxAttributeLen = max(MaxAttributeLen, HtmlAttributeInfo[i].nameLen);
        HtmlAttributeInfo[i].state = HtmlAttribute[i].state;
    }

    BongoKeywordIndexCreateFromTable(HtmlTagsIndex, HtmlTag, .name, TRUE);
    if (!HtmlTagsIndex) {
        MemFree(HtmlAttributeInfo);
        BongoKeywordIndexFree(HtmlAttributeIndex);
        return(FALSE);
    }

    elements = 0;
    while (HtmlTag[elements].name != NULL) {
        elements++;
    }

    HtmlTagInfo = MemMalloc(sizeof(HtmlTagsInfoStruct) * elements);

    if (!HtmlTagInfo)	{
        MemFree(HtmlAttributeInfo);
        BongoKeywordIndexFree(HtmlAttributeIndex);
        BongoKeywordIndexFree(HtmlTagsIndex);
        return(FALSE);
    }

    for (i = 0; i < elements; i++) {
        HtmlTagInfo[i].name = HtmlTag[i].name;
        HtmlTagInfo[i].nameLen = strlen(HtmlTag[i].name);
        MaxTagLen = max(MaxTagLen, HtmlTagInfo[i].nameLen);
        if (HtmlTag[i].endSupressTag) {
            HtmlTagInfo[i].endId = BongoKeywordBegins(HtmlTagsIndex, HtmlTag[i].endSupressTag);
        } else {
            HtmlTagInfo[i].endId = -1;
        }

        HtmlTagInfo[i].sendState = HtmlTag[i].sendState;
        HtmlTagInfo[i].supressState = HtmlTag[i].supressState;
        HtmlTagInfo[i].endSupressState = HtmlTag[i].endSupressState;
    }
    return(TRUE);
}

static BOOL
CodecHtmlShutdown(void)
{
    if (HtmlAttributeIndex) {
        BongoKeywordIndexFree(HtmlAttributeIndex);
        HtmlAttributeIndex = NULL;
    }

    if (HtmlAttributeInfo) {
        MemFree(HtmlAttributeInfo);
        HtmlAttributeInfo = NULL;
    }

    if (HtmlTagsIndex) {
        BongoKeywordIndexFree(HtmlTagsIndex);
        HtmlTagsIndex = NULL;
    }

    if (HtmlTagInfo) {
        MemFree(HtmlTagInfo);
        HtmlTagInfo = NULL;
    }

    return(TRUE);
}

__inline static void
HtmlDecodeFlushOutStream(unsigned long Space, unsigned char **Out, unsigned char *OutEnd, unsigned long Len, StreamStruct *Codec, StreamStruct *NextCodec)
{
    unsigned long len;

    if ((*Out + Space) < OutEnd) {
        return;
    }

    if ((Len == Codec->Len) && (Codec->EOS)) {
        NextCodec->EOS = TRUE;
        Codec->State = 0;
        Codec->StreamData2 = 0;
    }

    len = NextCodec->Codec(NextCodec, NextCodec->Next);
    if (len > 0) {
        NextCodec->Len -= len;
        memmove(NextCodec->Start, *Out - NextCodec->Len, NextCodec->Len);
        *Out = NextCodec->Start + NextCodec->Len;
    }
    return;
}


__inline static void
HtmlDecodeFlushNext(StreamStruct *Codec, StreamStruct *NextCodec, unsigned long Len, unsigned char *Out, FILE *debugHandle)
{
    if (NextCodec->Len > 0) {
        if (Codec->EOS) {
            if (Len == Codec->Len) {
                NextCodec->EOS = TRUE;
                Codec->State = 0;
                Codec->StreamData2 = 0;
            }
        }

        NextCodec->Len -= NextCodec->Codec(NextCodec, NextCodec->Next);
        memmove(NextCodec->Start, Out - NextCodec->Len, NextCodec->Len);
        CloseDebugLog(debugHandle);
        return;
    }

    if (Codec->EOS) {
        if (Len == Codec->Len) {
            NextCodec->EOS = TRUE;
            Codec->State = 0;
            Codec->StreamData2 = 0;
        }

        NextCodec->Codec(NextCodec, NextCodec->Next);
        CloseDebugLog(debugHandle);
        return;
    }

    CloseDebugLog(debugHandle);
    return;
}

__inline static void
CodecCopyChar(unsigned char **In, unsigned char **Out, unsigned long *Len, StreamStruct *NextCodec, FILE *debugHandle)
{
    CopyCharDebugLog(debugHandle, In);
    (*Out)[0] = (*In)[0];
    (*Out)++;
    (*In)++;
    (*Len)++;
    NextCodec->Len++;
}

__inline static void
CodecSkipChar(unsigned char **In, unsigned long *Len, FILE *debugHandle)
{
    SkipCharDebugLog(debugHandle, In);
    (*In)++;
    (*Len)++;
}

__inline static void
CodecInsertChar(unsigned char newChar, unsigned char **Out, StreamStruct *NextCodec, FILE *debugHandle)
{
    InsertCharDebugLog(debugHandle, newChar);
    (*Out)[0] = newChar;
    (*Out)++;
    NextCodec->Len++;
}

__inline static void
CodecCopyChars(unsigned long size, unsigned char **In, unsigned char **Out, unsigned long *Len, StreamStruct *NextCodec, FILE *debugHandle)
{
    CopyCharsDebugLog(debugHandle, size, In);
    memcpy(*Out, *In, size);
    (*Out) += size;							   
    (*In) += size;
    (*Len) += size;
    NextCodec->Len += size;
}

__inline static void
CodecSkipChars(unsigned long size, unsigned char **In, unsigned long *Len, FILE *debugHandle)
{
    SkipCharsDebugLog(debugHandle, size, In);
    (*In) += size;
    (*Len) += size;
}

__inline static void
CodecInsertChars(unsigned long size, unsigned char *buffer, unsigned char **Out, StreamStruct *NextCodec, FILE *debugHandle)
{
    InsertCharsDebugLog(debugHandle, size, buffer);
    memcpy(*Out, buffer, size);
    (*Out) += size;
    NextCodec->Len += size;
}

__inline static void
SendUrlMailto(unsigned char **In, unsigned char **Out, unsigned char *OutEnd, unsigned long *Len, StreamStruct *Codec, StreamStruct *NextCodec, FILE *debugHandle)
{
    unsigned char *mailToUrl;
    unsigned long mailToUrlLen;

    /* we need to replace the mailto url with our own */
    CodecSkipChars(sizeof("mailto:") - 1, In, Len, debugHandle);
    mailToUrl = (unsigned char *)Codec->URL + strlen(Codec->URL) + 1;
    mailToUrlLen = strlen(mailToUrl);
    HtmlDecodeFlushOutStream(mailToUrlLen, Out, OutEnd, *Len, Codec, NextCodec);
    CodecInsertChars(mailToUrlLen, mailToUrl, Out, NextCodec, debugHandle);
}

__inline static void
HtmlReplaceUrlJavascript(unsigned char **In, unsigned char **Out, unsigned char *OutEnd, unsigned long *Len, StreamStruct *Codec, StreamStruct *NextCodec, FILE *debugHandle)
{
    CodecSkipChars(sizeof("javascript:") - 1, In, Len, debugHandle);
    HtmlDecodeFlushOutStream(sizeof("bad:") - 1, Out, OutEnd, *Len, Codec, NextCodec);
    CodecInsertChars(sizeof("bad:") - 1, "bad:", Out, NextCodec, debugHandle);
}


/* browsers will execute inline javascript (src='myfunc()').  */
/* This function replaces '(' with '[' and ')' with ']' to prevent the javascript from executing */
/* javascript can be obviscated by using html entities (src='&#106;avascript:http://mybadcode.com/mybadcode.js) */
/* This function replaces '&' and ';' with '-' to prevent this */
__inline static long
HtmlSanitizeAndSendUrlUntil(unsigned char endChar, unsigned char **In, unsigned char **Out, unsigned char *OutEnd, unsigned long *Len, StreamStruct *Codec, StreamStruct *NextCodec, FILE *debugHandle)
{
    do {
        if (*Len < Codec->Len) {
            if (((endChar != ' ') && (**In != endChar)) || ((endChar == ' ') && (!isspace(**In)))) {
                if (**In != '>') {
                    if (**In != '(') {
                        if (**In != ')') {
                            if (**In != '&') {
                                if (**In != ';') {
                                    HtmlDecodeFlushOutStream(1, Out, OutEnd, *Len, Codec, NextCodec);
                                    CodecCopyChar(In, Out, Len, NextCodec, debugHandle);
                                    continue;
                                }
                            }
                            HtmlDecodeFlushOutStream(1, Out, OutEnd, *Len, Codec, NextCodec);
                            CodecSkipChar(In, Len, debugHandle);
                            CodecInsertChar('-', Out, NextCodec, debugHandle);
                            continue;
                        }
                        HtmlDecodeFlushOutStream(1, Out, OutEnd, *Len, Codec, NextCodec);
                        CodecSkipChar(In, Len, debugHandle);
                        CodecInsertChar(']', Out, NextCodec, debugHandle);
                        continue;
                    }
                    HtmlDecodeFlushOutStream(1, Out, OutEnd, *Len, Codec, NextCodec);
                    CodecSkipChar(In, Len, debugHandle);
                    CodecInsertChar('[', Out, NextCodec, debugHandle);
                    continue;
                }
                HtmlDecodeFlushOutStream(1, Out, OutEnd, *Len, Codec, NextCodec);
                CodecCopyChar(In, Out, Len, NextCodec, debugHandle);
                Codec->State = CODEC_HTML_SEND_CONTENT;
                break;
            }
            HtmlDecodeFlushOutStream(1, Out, OutEnd, *Len, Codec, NextCodec);
            CodecCopyChar(In, Out, Len, NextCodec, debugHandle);
            Codec->State = CODEC_HTML_SEND_TAG_SPACE;
            break;
        }

        return(0);
    } while(TRUE);
    return(1);
}

__inline static long
HtmlExamineUrlUntil(unsigned char endChar, unsigned char **In, unsigned char **Out, unsigned char **End, unsigned long *Len, StreamStruct *Codec, StreamStruct *NextCodec, FILE *debugHandle, unsigned long defaultState, unsigned long javascriptState, unsigned long mailtoState, unsigned long httpState)
{
    unsigned char *ptr;

    if (tolower(**In) != 'h') {
        if (tolower(**In) != 'm') {
            if (tolower(**In) != 'j') {
                Codec->State = defaultState;
                return(1);
            }

            if (*End > (*In + sizeof("javascript:") - 1)) {
                if (XplStrNCaseCmp(*In, "javascript:", sizeof("javascript:") - 1) == 0) {
                    Codec->State = javascriptState;
                    return(1);
                }

                Codec->State = defaultState;
                return(1);
            }

            ptr = *In;

            do {
                if (ptr < *End) {
                    if (*ptr != '>') {
                        if (((endChar != ' ') && (*ptr != endChar)) || ((endChar == ' ') && (!isspace(*ptr)))) {
                            ptr++;
                            continue;
                        }
                    }
                    break;
                }
                ptr = NULL;
                break;
            } while (TRUE);

            if (ptr || Codec->EOS) {
                /* We have the whole value */	
                Codec->State = defaultState;
                return(1);
            }

            /* Need more data */
            return(0);
        }

        if (Codec->URL) {
            if (*End > (*In + sizeof("mailto") - 1)) {
                if (XplStrNCaseCmp(*In, "mailto:", sizeof("mailto:") - 1) == 0) {
                    /* we need to replace the mailto url with our own */
                    Codec->State = mailtoState;
                    return(1);
                }

                Codec->State = defaultState;
                return(1);
            }

            ptr = *In;

            do {
                if (ptr < *End) {
                    if (*ptr != '>') {
                        if (((endChar != ' ') && (*ptr != endChar)) || ((endChar == ' ') && (!isspace(*ptr)))) {
                            ptr++;
                            continue;
                        }
                    }
                    break;
                }
                ptr = NULL;
                break;
            } while (TRUE);

            if (ptr || Codec->EOS) {
                /* We have the whole value */	
                Codec->State = defaultState;
                return(1);
            }

            /* Need more data */
            return(0);
        }

        Codec->State = defaultState;
        return(1);
    }

    if (*End > (*In + sizeof("http:") - 1)) {
        if (XplStrNCaseCmp(*In, "http:", sizeof("http:") - 1) == 0) {
            Codec->State = httpState;
            return(1);
        }

        Codec->State = defaultState;
        return(1);
    }

    ptr = *In;

    do {
        if (ptr < *End) {
            if (*ptr != '>') {
                if (((endChar != ' ') && (*ptr != endChar)) || ((endChar == ' ') && (!isspace(*ptr)))) {
                    ptr++;
                    continue;
                }
            }
            break;
        }
        ptr = NULL;
        break;
    } while (TRUE);

    if (ptr || Codec->EOS) {
        /* We have the whole value */	
        Codec->State = defaultState;
        return(1);
    }

    /* Need more data */
    return(0);
}

static int
HTML_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
    unsigned char	*In = Codec->Start;
    unsigned char	*Out = NextCodec->Start + NextCodec->Len;
    unsigned char	*OutEnd = NextCodec->End;
    unsigned char	*End = In + Codec->Len;
    unsigned long	Len = 0;
    unsigned char *closeTagChar= NULL;
    FILE *debugHandle = NULL;

    OpenDebugLog(debugHandle, HTML_CODEC_DEBUG_FILE_PATH);
    do {
        ChangeStateDebugLog(debugHandle, StateString[Codec->State]);
        switch (Codec->State) {
        case CODEC_HTML_SEND_CONTENT: {
            /* Pass through the content between tags */
            do {
                if (Len < Codec->Len) {
                    if (In[0] != '<') {
                        HtmlDecodeFlushOutStream(1, &Out, OutEnd, Len, Codec, NextCodec);
                        CodecCopyChar(&In, &Out, &Len, NextCodec, debugHandle);
                        continue;
                    }
                    Codec->State = CODEC_HTML_SEND_TAG_EXAMINE;	
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SEND_TAG_EXAMINE: {
            /* Look up the current tag and change state if appropriate */
            long tagId;

            closeTagChar = In + 1;

            do {
                if (closeTagChar < End) {
                    if (*closeTagChar != '>') {
                        closeTagChar++;
                        continue;
                    }

                    closeTagChar++;

                    break;
                }
                closeTagChar = NULL;
                break;
            } while (TRUE);

            if (closeTagChar || Codec->EOS || ((Codec->Len - Len) > MaxTagLen)) {
                /* look up tag */

                tagId = BongoKeywordBegins(HtmlTagsIndex, In);
                if (tagId == -1) {

                    Codec->State = CODEC_HTML_SEND_TAG;	
                    continue;

                }

                Codec->State = HtmlTagInfo[tagId].sendState;
                Codec->StreamData2 = (void *)HtmlTagInfo[tagId].endId;
                continue;
            }

            /* Need more data */
            if ((Len > 0) || (Codec->End > (Codec->Start + Codec->Len))) {
                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            }

            /* 
               If we get here, we didn't have stuff to flush 
               and our buffer is too small to contain the whole tag.
            */
				
            Codec->State = CODEC_HTML_SKIP_TAG;	
            continue;
        }

        case CODEC_HTML_SEND_TAG: {
            /* Pass through the tag name */
            do {
                if (Len < Codec->Len) {
                    HtmlDecodeFlushOutStream(1, &Out, OutEnd, Len, Codec, NextCodec);
                    CodecCopyChar(&In, &Out, &Len, NextCodec, debugHandle);
                    if (In[-1] != '>') {
                        if (!isspace(In[-1])) {
                            continue;
                        }

                        Codec->State = CODEC_HTML_SEND_TAG_SPACE;
                        break;
                    }

                    Codec->State = CODEC_HTML_SEND_CONTENT;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SEND_TAG_SPACE: {
            /* Pass white space inside a tag */
            do {
                if (Len < Codec->Len) {

                    if (!isspace(In[0])) {
                        if (In[0] != '>') {
                            Codec->State = CODEC_HTML_SEND_TAG_ATTRIBUTE_EXAMINE;
                            break;
                        }

                        HtmlDecodeFlushOutStream(1, &Out, OutEnd, Len, Codec, NextCodec);
                        CodecCopyChar(&In, &Out, &Len, NextCodec, debugHandle);
                        Codec->State = CODEC_HTML_SEND_CONTENT;
                        break;
                    }
						 
                    HtmlDecodeFlushOutStream(1, &Out, OutEnd, Len, Codec, NextCodec);
                    CodecCopyChar(&In, &Out, &Len, NextCodec, debugHandle);
                    continue;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SEND_TAG_ATTRIBUTE_EXAMINE: {
            /* Look up tag attribute and set the appropriate state */
            long attributeId;
            unsigned char *equalSignChar;

            equalSignChar = In + 1;

            do {
                if (equalSignChar < End) {
                    if (*equalSignChar != '=') {
                        equalSignChar++;
                        continue;
                    }

                    break;
                }
                equalSignChar = NULL;
                break;
            } while (TRUE);

            if (equalSignChar || Codec->EOS || ((Codec->Len - Len) > MaxAttributeLen)) {
                /* look up attribute */

                attributeId = BongoKeywordBegins(HtmlAttributeIndex, In);
                if (attributeId == -1) {
                    Codec->State = CODEC_HTML_SEND_TAG_ATTRIBUTE_NAME;
                    continue;

                }

                Codec->State = HtmlAttributeInfo[attributeId].state;
                continue;
            }

            /* Need more data */
            if ((Len > 0) || (Codec->End > (Codec->Start + Codec->Len))) {
                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            }

            /* 
               If we get here, we didn't have stuff to flush 
               and our buffer is too small to contain the whole tag.
            */
				
            Codec->State = CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME;	
            continue;
        }

        case CODEC_HTML_SEND_TAG_ATTRIBUTE_NAME: {
            /* Pass through the attribute name */
            do {
                if (Len < Codec->Len) {
                    HtmlDecodeFlushOutStream(1, &Out, OutEnd, Len, Codec, NextCodec);
                    CodecCopyChar(&In, &Out, &Len, NextCodec, debugHandle);
                    if (In[-1] != '>') {
                        if (In[-1] != '=') {
                            continue;
                        }

                        Codec->State = CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE;
                        break;
                    }

                    Codec->State = CODEC_HTML_SEND_CONTENT;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE: {
            /* Find the beginning of the attribute value and determine if it is quoted */
            do {
                if (Len < Codec->Len) {
                    HtmlDecodeFlushOutStream(1, &Out, OutEnd, Len, Codec, NextCodec);
                    CodecCopyChar(&In, &Out, &Len, NextCodec, debugHandle);

                    if (In[-1] == '"') {
                        Codec->State = CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE_DOUBLE_QUOTED;
                        break;
                    }

                    if (In[-1] == '\'') {
                        Codec->State = CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE_SINGLE_QUOTED;
                        break;
                    }

                    if (In[-1] != '>') {
                        if (!isspace(In[-1])) {
                            Codec->State = CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE_TEXT;
                            break;
                        }

                        continue;
                    }

                    Codec->State = CODEC_HTML_SEND_CONTENT;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE_DOUBLE_QUOTED: {
            /* Pass through double quoted attribute value */
            do {
                if (Len < Codec->Len) {
                    HtmlDecodeFlushOutStream(1, &Out, OutEnd, Len, Codec, NextCodec);
                    CodecCopyChar(&In, &Out, &Len, NextCodec, debugHandle);

                    if (In[-1] != '"') {
                        if (In[-1] != '>') {
                            continue;
                        }	

                        Codec->State = CODEC_HTML_SEND_CONTENT;
                        break;
                    }

                    Codec->State = CODEC_HTML_SEND_TAG_SPACE;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE_SINGLE_QUOTED: {
            /* Pass through single quoted attribute value */
            do {
                if (Len < Codec->Len) {
                    HtmlDecodeFlushOutStream(1, &Out, OutEnd, Len, Codec, NextCodec);
                    CodecCopyChar(&In, &Out, &Len, NextCodec, debugHandle);

                    if (In[-1] != '\'') {
                        if (In[-1] != '>') {
                            continue;
                        }	

                        Codec->State = CODEC_HTML_SEND_CONTENT;
                        break;
                    }

                    Codec->State = CODEC_HTML_SEND_TAG_SPACE;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE_TEXT: {
            /* Pass through an un-quoted attribute value */
            do {
                if (Len < Codec->Len) {
                    HtmlDecodeFlushOutStream(1, &Out, OutEnd, Len, Codec, NextCodec);
                    CodecCopyChar(&In, &Out, &Len, NextCodec, debugHandle);

                    if (!isspace(In[-1])) {
                        if (In[-1] != '>') {
                            continue;
                        }	

                        Codec->State = CODEC_HTML_SEND_CONTENT;
                        break;
                    }

                    Codec->State = CODEC_HTML_SEND_TAG_SPACE;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SEND_TAG_URL_NAME: {
            /* Pass through url attribute name (href=, src=, etc.) */
            do {
                if (Len < Codec->Len) {
                    HtmlDecodeFlushOutStream(1, &Out, OutEnd, Len, Codec, NextCodec);
                    CodecCopyChar(&In, &Out, &Len, NextCodec, debugHandle);
                    if (In[-1] != '>') {
                        if (In[-1] != '=') {
                            continue;
                        }

                        Codec->State = CODEC_HTML_SEND_TAG_URL_PRE_VALUE;
                        break;
                    }

                    Codec->State = CODEC_HTML_SEND_CONTENT;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SEND_TAG_URL_PRE_VALUE: {
            /* Pass through any white space before the url value */
            do {
                if (Len < Codec->Len) {
                    if (!isspace(*In)) {
                        Codec->State = CODEC_HTML_SEND_TAG_URL_VALUE;
                        break;
                    }

                    HtmlDecodeFlushOutStream(1, &Out, OutEnd, Len, Codec, NextCodec);
                    CodecCopyChar(&In, &Out, &Len, NextCodec, debugHandle);
                    continue;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SEND_TAG_URL_VALUE: {
            /* determine if the value quoted and set the appropriate state */
            if (Len < Codec->Len) {
                HtmlDecodeFlushOutStream(1, &Out, OutEnd, Len, Codec, NextCodec);
                CodecCopyChar(&In, &Out, &Len, NextCodec, debugHandle);

                if (In[-1] == '"') {
                    Codec->State = CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED_EXAMINE;
                    continue;
                }

                if (In[-1] == '\'') {
                    Codec->State = CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED_EXAMINE;
                    continue;
                }

                if (In[-1] != '>') {
                    Codec->State = CODEC_HTML_SEND_TAG_URL_VALUE_TEXT_EXAMINE;
                    continue;
                }

                Codec->State = CODEC_HTML_SEND_CONTENT;
                continue;
            }

            HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
            return(Len);
        }

        case CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED_EXAMINE: {
            /* look up url prefix for a double quoted url and set the appropriate state */
            if (HtmlExamineUrlUntil('"', &In, &Out, &End, &Len, Codec, NextCodec, debugHandle, 
                                    CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED,            /* default    */
                                    CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED_JAVASCRIPT, /* javascript */
                                    CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED_MAILTO, 	  /* mailto     */
                                    CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE_DOUBLE_QUOTED)) {   /* http       */
                continue;
            }

            HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
            return(Len);
        }

        case CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED_EXAMINE: {
            /* look up url prefix for a single quoted url and set the appropriate state */
            if (HtmlExamineUrlUntil('\'', &In, &Out, &End, &Len, Codec, NextCodec, debugHandle, 
                                    CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED,            /* default    */
                                    CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED_JAVASCRIPT, /* javascript */
                                    CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED_MAILTO, 	  /* mailto     */
                                    CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE_SINGLE_QUOTED)) {   /* http       */
                continue;
            }

            HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
            return(Len);
        }

        case CODEC_HTML_SEND_TAG_URL_VALUE_TEXT_EXAMINE: {
            /* look up url prefix for an unquoted url and set the appropriate state */
            if (HtmlExamineUrlUntil(' ', &In, &Out, &End, &Len, Codec, NextCodec, debugHandle, 
                                    CODEC_HTML_SEND_TAG_URL_VALUE_TEXT,            /* default    */
                                    CODEC_HTML_SEND_TAG_URL_VALUE_TEXT_JAVASCRIPT, /* javascript */
                                    CODEC_HTML_SEND_TAG_URL_VALUE_TEXT_MAILTO, 	  /* mailto     */
                                    CODEC_HTML_SEND_TAG_ATTRIBUTE_VALUE_TEXT)) {   /* http       */
                continue;
            }

            HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
            return(Len);
        }

        case CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED: {
            /* Pass through the remainder of a double quoted url breaking any inline javascript	on the way */
            if (HtmlSanitizeAndSendUrlUntil('"', &In, &Out, OutEnd, &Len, Codec, NextCodec, debugHandle)) {
                continue;
            }
            HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
            return(Len);
        }

        case CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED_JAVASCRIPT: {
            /* Replace the javascript: prefix in a doubled quoted url */
            HtmlReplaceUrlJavascript(&In, &Out, OutEnd, &Len, Codec, NextCodec, debugHandle);
            Codec->State = CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED;
            continue;
        }

        case CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED_MAILTO: {
            /* Replace the mailto: prefix in a doubled quoted url */
            SendUrlMailto(&In, &Out, OutEnd, &Len, Codec, NextCodec, debugHandle);

            Codec->State = CODEC_HTML_SEND_TAG_URL_VALUE_DOUBLE_QUOTED;
            continue;
        }

        case CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED: {
            /* Pass through the remainder of a single quoted url breaking any inline javascript	on the way */
            if (HtmlSanitizeAndSendUrlUntil('\'', &In, &Out, OutEnd, &Len, Codec, NextCodec, debugHandle)) {
                continue;
            }
            HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
            return(Len);
        }

        case CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED_JAVASCRIPT: {
            /* Replace the javascript: prefix in a single quoted url */
            HtmlReplaceUrlJavascript(&In, &Out, OutEnd, &Len, Codec, NextCodec, debugHandle);
            Codec->State = CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED;
            continue;
        }

        case CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED_MAILTO: {
            /* Replace the mailto: prefix in a single quoted url */
            SendUrlMailto(&In, &Out, OutEnd, &Len, Codec, NextCodec, debugHandle);

            Codec->State = CODEC_HTML_SEND_TAG_URL_VALUE_SINGLE_QUOTED;
            continue;
        }

        case CODEC_HTML_SEND_TAG_URL_VALUE_TEXT: {
            /* Pass through the remainder of a unquoted url breaking any inline javascript	on the way */
            if (HtmlSanitizeAndSendUrlUntil(' ', &In, &Out, OutEnd, &Len, Codec, NextCodec, debugHandle)) {
                continue;
            }
            HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
            return(Len);
        }

        case CODEC_HTML_SEND_TAG_URL_VALUE_TEXT_JAVASCRIPT: {
            /* Replace the javascript: prefix in an unquoted url */
            HtmlReplaceUrlJavascript(&In, &Out, OutEnd, &Len, Codec, NextCodec, debugHandle);
            Codec->State = CODEC_HTML_SEND_TAG_URL_VALUE_TEXT;
            continue;
        }

        case CODEC_HTML_SEND_TAG_URL_VALUE_TEXT_MAILTO: {
            /* Replace the mailto: prefix in an unquoted url */
            SendUrlMailto(&In, &Out, OutEnd, &Len, Codec, NextCodec, debugHandle);

            Codec->State = CODEC_HTML_SEND_TAG_URL_VALUE_TEXT;
            continue;
        }

        case CODEC_HTML_SKIP_TAG_ATTRIBUTE_NAME: {
            /* skip an attribute name */
            do {
                if (Len < Codec->Len) {
                    CodecSkipChar(&In, &Len, debugHandle);

                    if (In[-1] != '>') {
                        if (In[-1] != '=') {
                            continue;
                        }

                        Codec->State = CODEC_HTML_SKIP_TAG_ATTRIBUTE_VALUE;
                        break;
                    }

                    Codec->State = CODEC_HTML_SEND_CONTENT;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SKIP_TAG_ATTRIBUTE_VALUE: {
            /* skip any white space and the first character of the attribute value */
            do {
                if (Len < Codec->Len) {
                    CodecSkipChar(&In, &Len, debugHandle);
                    if (In[-1] == '"') {
                        Codec->State = CODEC_HTML_SKIP_TAG_ATTRIBUTE_VALUE_DOUBLE_QUOTED;
                        break;
                    }

                    if (In[-1] == '\'') {
                        Codec->State = CODEC_HTML_SKIP_TAG_ATTRIBUTE_VALUE_SINGLE_QUOTED;
                        break;
                    }

                    if (In[-1] != '>') {
                        if (!isspace(In[-1])) {
                            Codec->State = CODEC_HTML_SKIP_TAG_ATTRIBUTE_VALUE_TEXT;
                            break;
                        }

                        continue;
                    }

                    Codec->State = CODEC_HTML_SEND_CONTENT;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SKIP_TAG_ATTRIBUTE_VALUE_DOUBLE_QUOTED: {
            /* skip the rest of a double quoted attribute value */
            do {
                if (Len < Codec->Len) {
                    CodecSkipChar(&In, &Len, debugHandle);

                    if (In[-1] != '"') {
                        if (In[-1] != '>') {
                            continue;
                        }	

                        Codec->State = CODEC_HTML_SEND_CONTENT;
                        break;
                    }

                    Codec->State = CODEC_HTML_SEND_TAG_SPACE;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SKIP_TAG_ATTRIBUTE_VALUE_SINGLE_QUOTED: {
            /* skip the rest of a single quoted attribute value */
            do {
                if (Len < Codec->Len) {
                    CodecSkipChar(&In, &Len, debugHandle);

                    if (In[-1] != '\'') {
                        if (In[-1] != '>') {
                            continue;
                        }	

                        Codec->State = CODEC_HTML_SEND_CONTENT;
                        break;
                    }

                    Codec->State = CODEC_HTML_SEND_TAG_SPACE;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SKIP_TAG_ATTRIBUTE_VALUE_TEXT: {
            /* skip the rest of a single quoted attribute value */
            do {
                if (Len < Codec->Len) {
                    CodecSkipChar(&In, &Len, debugHandle);

                    if (!isspace(In[-1])) {
                        if (In[-1] != '>') {
                            continue;
                        }	

                        Codec->State = CODEC_HTML_SEND_CONTENT;
                        break;
                    }

                    Codec->State = CODEC_HTML_SEND_TAG_SPACE;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SKIP_TAG: {
            /* skip the rest of a double quoted attribute value */
            do {
                if (Len < Codec->Len) {
                    if (In[0] != '>') {
                        CodecSkipChar(&In, &Len, debugHandle);
                        continue;
                    }

                    CodecSkipChar(&In, &Len, debugHandle);
                    Codec->State = CODEC_HTML_SEND_CONTENT;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SUPRESS_CONTENT: {
            /* skip everything up to the next tag */
            do {
                if (Len < Codec->Len) {
                    if (In[0] != '<') {
                        CodecSkipChar(&In, &Len, debugHandle);
                        continue;
                    }
                    Codec->State = CODEC_HTML_SUPRESS_TAG_EXAMINE;	
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SUPRESS_TAG_EXAMINE: {
            /* lookup tag to see if it is the end tag. */
            /* if it is stop suppressing content */
            long tagId;

            closeTagChar = In + 1;

            do {
                if (closeTagChar < End) {
                    if (*closeTagChar != '>') {
                        closeTagChar++;
                        continue;
                    }
                    break;
                }
                closeTagChar = NULL;
                break;
            } while (TRUE);

            if (closeTagChar || Codec->EOS || ((Codec->Len - Len) > MaxTagLen)) {
                /* look up tag */

                tagId = BongoKeywordBegins(HtmlTagsIndex, In);
                if (tagId == -1) {
                    Codec->State = CODEC_HTML_SUPRESS_TAG;	
                    continue;

                }

                if (tagId != (long)Codec->StreamData2)	{
                    Codec->State = HtmlTagInfo[tagId].supressState;	
                    continue;
                }


                Codec->State = HtmlTagInfo[tagId].endSupressState;	
                Codec->StreamData2 = 0;
                continue;
            }

            /* Need more data */
            if ((Len > 0) || (Codec->End > (Codec->Start + Codec->Len))) {
                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            }

            /* 
               If we get here, we didn't have stuff to flush 
               and our buffer is too small to contain the whole tag.
            */
				
            Codec->State = CODEC_HTML_SUPRESS_TAG;	
            continue;
        }

        case CODEC_HTML_SUPRESS_TAG: {
            /* skip the current tag and stay in suppressed mode */
            do {
                if (Len < Codec->Len) {
                    if (In[0] != '>') {
                        CodecSkipChar(&In, &Len, debugHandle);
                        continue;
                    }
                    CodecSkipChar(&In, &Len, debugHandle);

                    Codec->State = CODEC_HTML_SUPRESS_CONTENT;	
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SUPRESS_META: {
            /* if we are supressing meta the browser will not know */
            /* about the charset.  We need to know the charset */
            /* so that we can translate the content into utf-8 */
            if (Codec->Charset) {
                do {
                    if (Len < Codec->Len) {
                        if (*In != '>') {
                            if (tolower(*In) != 'c') {
                                CodecSkipChar(&In, &Len, debugHandle);
                                continue;
                            }

                            Codec->State = CODEC_HTML_SUPRESS_META_CHARSET;
                            break;
                        }

                        CodecSkipChar(&In, &Len, debugHandle);
                        Codec->State = CODEC_HTML_SUPRESS_CONTENT;
                        break;
                    }

                    HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                    return(Len);
                } while(TRUE);
                continue;
            }

            /* if there is not a Charset Codec we can't do anything even if we knew the charset */
            Codec->State = CODEC_HTML_SUPRESS_TAG;
            continue;
        }

        case CODEC_HTML_SUPRESS_META_CHARSET: {
            /* determine if the curent stream content is 'charset' */
            if (End > (In + sizeof("charset=\"\"") - 1)) {
                if (XplStrNCaseCmp(In, "charset", sizeof("charset") - 1) == 0) {
                    if (In[sizeof("charset") - 1] == '=') {
                        if (In[sizeof("charset=") - 1] != '>') {
                            if (In[sizeof("charset=") - 1] != '\'') {
                                if (In[sizeof("charset=") - 1] != '"') {
                                    if (!isspace(In[sizeof("charset=") - 1])) {
                                        CodecSkipChars(sizeof("charset=") - 1, &In, &Len, debugHandle);
                                        Codec->State = CODEC_HTML_SUPRESS_META_CHARSET_VALUE;
                                        continue;
                                    }

                                    CodecSkipChars(sizeof("charset= ") - 1, &In, &Len, debugHandle);
                                    Codec->State = CODEC_HTML_SUPRESS_META_CHARSET_PRE_VALUE;
                                    continue;
                                }
                            }

                            CodecSkipChars(sizeof("charset=\'") - 1, &In, &Len, debugHandle);
                            Codec->State = CODEC_HTML_SUPRESS_META_CHARSET_VALUE;
                            continue;
                        }

                        CodecSkipChars(sizeof("charset=>") - 1, &In, &Len, debugHandle);
                        Codec->State = CODEC_HTML_SUPRESS_CONTENT;
                        continue;
                    }
						
                    if (isspace(In[sizeof("charset") - 1])) {
                        CodecSkipChars(sizeof("charset ") - 1, &In, &Len, debugHandle);
                        Codec->State = CODEC_HTML_SUPRESS_META_CHARSET_PRE_EQUAL;
                        continue;
                    }
                }

                CodecSkipChar(&In, &Len, debugHandle);
                Codec->State = CODEC_HTML_SUPRESS_META;
                continue;
            }

            /* Need more data */
            if (!Codec->EOS) {
                return(Len);
            }

            Codec->State = CODEC_HTML_SUPRESS_TAG;
            continue;
        }

        case CODEC_HTML_SUPRESS_META_CHARSET_PRE_EQUAL: {
            /* supress white space up through the equal */
            do {
                if (Len < Codec->Len) {
                    CodecSkipChar(&In, &Len, debugHandle);
                    if (In[-1] == '=') {
                        Codec->State = CODEC_HTML_SUPRESS_META_CHARSET_PRE_VALUE;
                        break;
                    }

                    if (isspace(In[-1])) {
                        continue;
                    }

                    if (In[-1] == '>') {
                        Codec->State = CODEC_HTML_SUPRESS_CONTENT;
                        break;
                    }

                    Codec->State = CODEC_HTML_SUPRESS_TAG;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SUPRESS_META_CHARSET_PRE_VALUE: {
            /* supress white space after equal */
            do {
                if (Len < Codec->Len) {
                    if (*In != '>') {
                        if (!isspace(*In)) {
                            Codec->State = CODEC_HTML_SUPRESS_META_CHARSET_VALUE;
                            break;
                        }

                        CodecSkipChar(&In, &Len, debugHandle);
                        continue;
                    }

                    CodecSkipChar(&In, &Len, debugHandle);
                    Codec->State = CODEC_HTML_SUPRESS_CONTENT;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SUPRESS_META_CHARSET_VALUE: {
            /* get the entire charset value in the buffer and look up the appropriate codec */
            unsigned char *endValue;

            endValue = In;

            do {
                if (endValue < End) {
                    if (*endValue != '"') {
                        if (*endValue != '\'') {
                            if (*endValue != '>') {
                                if (!isspace(*endValue)) {
                                    endValue++;
                                    continue;
                                }
                            }
                        }
                    }

                    break;
                }
                endValue = NULL;
                break;
            } while (TRUE);

            if (endValue) {
                unsigned char tmpChar;
                StreamCodecFunc	Func;
	
                tmpChar = *endValue;
                *endValue = '\0';
                Func = FindCodec(In, FALSE);
                *endValue = tmpChar;
                if (Func) {
                    /* the existence of Codec->Charset is ensured in CODEC_HTML_SUPRESS_META */
                    ((StreamStruct *)(Codec->Charset))->Codec = Func;
                }

                CodecSkipChars(endValue - In, &In, &Len, debugHandle);
                Codec->State = CODEC_HTML_SUPRESS_TAG;
                continue;
            }

            if ((!Codec->EOS) && ((End - In) < MAX_CHARSET_SIZE)) {
                /* go get more */
                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            }

            /* If we get here, the chaset value is ill formed or bigger than any we know about. */
            Codec->State = CODEC_HTML_SUPRESS_TAG;
            continue;
        }

        case CODEC_HTML_SKIP_META: {
            /* if we are supressing meta the browser will not know */
            /* about the charset.  We need to know the charset */
            /* so that we can translate the content into utf-8 */
            if (Codec->Charset) {
                do {
                    if (Len < Codec->Len) {
                        if (*In != '>') {
                            if (tolower(*In) != 'c') {
                                CodecSkipChar(&In, &Len, debugHandle);
                                continue;
                            }

                            Codec->State = CODEC_HTML_SKIP_META_CHARSET;
                            break;
                        }

                        CodecSkipChar(&In, &Len, debugHandle);
                        Codec->State = CODEC_HTML_SEND_CONTENT;
                        break;
                    }

                    HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                    return(Len);
                } while(TRUE);
                continue;
            }

            /* if there is not a Charset Codec we can't do anything even if we knew the charset */
            Codec->State = CODEC_HTML_SKIP_TAG;
            continue;
        }

        case CODEC_HTML_SKIP_META_CHARSET: {
            /* determine if the curent stream content is 'charset' */
            if (End > (In + sizeof("charset=\"\"") - 1)) {
                if (XplStrNCaseCmp(In, "charset", sizeof("charset") - 1) == 0) {
                    if (In[sizeof("charset") - 1] == '=') {
                        if (In[sizeof("charset=") - 1] != '>') {
                            if (In[sizeof("charset=") - 1] != '\'') {
                                if (In[sizeof("charset=") - 1] != '"') {
                                    if (!isspace(In[sizeof("charset=") - 1])) {
                                        CodecSkipChars(sizeof("charset=") - 1, &In, &Len, debugHandle);
                                        Codec->State = CODEC_HTML_SKIP_META_CHARSET_VALUE;
                                        continue;
                                    }

                                    CodecSkipChars(sizeof("charset= ") - 1, &In, &Len, debugHandle);
                                    Codec->State = CODEC_HTML_SKIP_META_CHARSET_PRE_VALUE;
                                    continue;
                                }
                            }

                            CodecSkipChars(sizeof("charset=\'") - 1, &In, &Len, debugHandle);
                            Codec->State = CODEC_HTML_SKIP_META_CHARSET_VALUE;
                            continue;
                        }

                        CodecSkipChars(sizeof("charset=>") - 1, &In, &Len, debugHandle);
                        Codec->State = CODEC_HTML_SEND_CONTENT;
                        continue;
                    }
						
                    if (isspace(In[sizeof("charset") - 1])) {
                        CodecSkipChars(sizeof("charset ") - 1, &In, &Len, debugHandle);
                        Codec->State = CODEC_HTML_SKIP_META_CHARSET_PRE_EQUAL;
                        continue;
                    }
                }

                CodecSkipChar(&In, &Len, debugHandle);
                Codec->State = CODEC_HTML_SKIP_META;
                continue;
            }

            /* Need more data */
            if (!Codec->EOS) {
                return(Len);
            }

            Codec->State = CODEC_HTML_SKIP_TAG;
            continue;
        }

        case CODEC_HTML_SKIP_META_CHARSET_PRE_EQUAL: {
            /* supress white space up through the equal */
            do {
                if (Len < Codec->Len) {
                    CodecSkipChar(&In, &Len, debugHandle);
                    if (In[-1] == '=') {
                        Codec->State = CODEC_HTML_SKIP_META_CHARSET_PRE_VALUE;
                        break;
                    }

                    if (isspace(In[-1])) {
                        continue;
                    }

                    if (In[-1] == '>') {
                        Codec->State = CODEC_HTML_SEND_CONTENT;
                        break;
                    }

                    Codec->State = CODEC_HTML_SKIP_TAG;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SKIP_META_CHARSET_PRE_VALUE: {
            /* supress white space after equal */
            do {
                if (Len < Codec->Len) {
                    if (*In != '>') {
                        if (!isspace(*In)) {
                            Codec->State = CODEC_HTML_SKIP_META_CHARSET_VALUE;
                            break;
                        }

                        CodecSkipChar(&In, &Len, debugHandle);
                        continue;
                    }

                    CodecSkipChar(&In, &Len, debugHandle);
                    Codec->State = CODEC_HTML_SEND_CONTENT;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_SKIP_META_CHARSET_VALUE: {
            /* get the entire charset value in the buffer and look up the appropriate codec */
            unsigned char *endValue;

            endValue = In;

            do {
                if (endValue < End) {
                    if (*endValue != '"') {
                        if (*endValue != '\'') {
                            if (*endValue != '>') {
                                if (!isspace(*endValue)) {
                                    endValue++;
                                    continue;
                                }
                            }
                        }
                    }

                    break;
                }
                endValue = NULL;
                break;
            } while (TRUE);

            if (endValue) {
                unsigned char tmpChar;
                StreamCodecFunc	Func;
	
                tmpChar = *endValue;
                *endValue = '\0';
                Func = FindCodecDecoder(In);
                *endValue = tmpChar;
                if (Func) {
                    /* the existence of Codec->Charset is ensured in CODEC_HTML_SKIP_META */
                    ((StreamStruct *)(Codec->Charset))->Codec = Func;
                }

                CodecSkipChars(endValue - In, &In, &Len, debugHandle);
                Codec->State = CODEC_HTML_SKIP_TAG;
                continue;
            }

            if ((!Codec->EOS) && ((End - In) < MAX_CHARSET_SIZE)) {
                /* go get more */
                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            }

            /* If we get here, the chaset value is ill formed or bigger than any we know about. */
            Codec->State = CODEC_HTML_SKIP_TAG;
            continue;
        }

        }

        HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
        return(Len);
    } while(TRUE);
}


#define	LINK_MAILTO 1
#define	LINK_HTTP   2
#define	LINK_URL    3
#define	LINK_IMG    4

#define	MINIMUM_HTML_PROCESS_SIZE	50

/*
	This takes a text message and makes it viewable
	including making links clickable
*/

/* 
	This takes a plaintext message and makes it viewable/clickable via HTML

	URL should be set to the url of the server to use for the redirects
	Charset should be set to the charset of the user before starting the stream
*/

static int
HTML_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned char	*End=In+Codec->Len;
	unsigned long	Len=0;
	unsigned char	*BlockStart=In;
	unsigned char	*BlockEnd=In;
	unsigned long	i;
	unsigned long	j;
	unsigned long	k;
	BOOL				Go;

	while (Len<Codec->Len) {
		if (Codec->URL) {		 /* if this check is removed, the check would need to be re-added to the switch below.  */

			FlushOutStream(6);

			/* Find the end of the block */

			if (In>=BlockEnd) {
				if (Codec->State>0) {
					Codec->State=0;
					memcpy(Out, "</A>", 4);
					Out+=4;
					NextCodec->Len+=4;
					FlushOutStream(6);
				}
				BlockStart=In;
				BlockEnd=BlockStart+1;
				Go=TRUE;
				while (Go && BlockEnd<End) {
					switch(BlockEnd[0]) {
						case '\r':
						case '\n':
						case ' ':
						case '<':
						case '>':
						case '(':
						case ')': {
							Go=FALSE;
							continue;
						}

						case '.': {
							if ((BlockEnd+1)<End && isspace(BlockEnd[1])) {
								Go=FALSE;
								continue;
							}
							/* Fall-through */
						}

						default: {
							BlockEnd++;
							continue;
						}
					}
				}

				if (BlockEnd>=End) {
					/* We ran out of data */
					if (!Codec->EOS && ((Len>MINIMUM_HTML_PROCESS_SIZE) || ((unsigned long)(Codec->End-Codec->Start-MINIMUM_HTML_PROCESS_SIZE)>Codec->Len))) {
						/* Get more data */
					    if (Len >= Codec->Min) {
						return(Len);
					    }

					    return(Codec->Len);
					}
				}

				Go=TRUE;
				while (Go && BlockStart<BlockEnd) {
					switch(BlockStart[0]) {
						case '\r':
						case '\n':
						case ' ':
						case '.':
						case '<':
						case '>':
						case '(':
						case ')': {
							BlockStart++;
							continue;
						}

						default: {
							Go=FALSE;
							continue;
						}
					}
				}

				/*
					We found a block, analyze and send off 
					a) we look for an @-sign and a period within the block
					b)	the character sequence www
					c)	the character sequence ://

					if we find a) we will generate a mailto: url
					if we find b) we add the http:
				*/
			}

			if (In==BlockStart) {
				j=BlockEnd-BlockStart;

				/* The FlushOutStream calls in this section need to do whatever space required+1 so we have enough below */
				for (i=0; i<j; i++) {
					switch (BlockStart[i]) {
						case '@': {
							for (k=i+1; k<j; k++) {
								if (BlockStart[k]=='.') {
									unsigned char	*ptr;
									unsigned long	Len=0;

									/* We found an email address */
#if 0
									if (!Codec->URL) {
										i=j;
										break;
									}
#endif

									Codec->State=LINK_MAILTO;
									i=strlen(Codec->URL);
									ptr=(unsigned char *)(Codec->URL)+i+1;

									i=strlen(ptr);
									if (Codec->StreamData2) {
										Len=strlen(Codec->StreamData2);
									}

									/* Need to flush in pieces, BlockEnd-BlockStart might be the whole buffer */
									FlushOutStream(19+i+Len);
								
									memcpy(Out, "<A ", 3);
									Out+=3;

									if (Codec->StreamData2) {
										memcpy(Out, Codec->StreamData2, Len);
										Out+=Len;
									}

									/* Remove the http to allow relative links for http & https */
									//memcpy(Out, " HREF=\"http:", 12);
									memcpy(Out, " HREF=\"", 7);
									Out+=7;
									NextCodec->Len+=10+Len;

									memcpy(Out, ptr, i);
									Out+=i;
									NextCodec->Len+=i;

									i=BlockEnd-BlockStart;
									FlushOutStream(i);
									memcpy(Out, BlockStart, i);
									Out+=i;
									NextCodec->Len+=i;

									FlushOutStream(6);
									Out[0]='"';
									Out[1]='>';
									Out+=2;
									NextCodec->Len+=2;

									i=j;
								}
							}
							continue;
						}

						case 'w':
						case 'W': {
							if (j>2 && ((BlockStart+i+2)<BlockEnd)) {
								if ((toupper(BlockStart[i+1])=='W') && (toupper(BlockStart[i+2])=='W')) {
#if 0
									if (!Codec->URL) {
										i=j;
										break;
									}
#endif

									Codec->State=LINK_HTTP;
									i=strlen(Codec->URL);

									FlushOutStream(32+i);			/* 23+7+safety+i */

									memcpy(Out, "<A TARGET=\"__eb\" HREF=\"", 23);
									Out+=23;
									NextCodec->Len+=23;

									memcpy(Out, Codec->URL, i);
									Out+=i;
									NextCodec->Len+=i;

									memcpy(Out, "http://", 7);
									Out+=7;
									NextCodec->Len+=7;

									i=BlockEnd-BlockStart;

									FlushOutStream(i);

									memcpy(Out, BlockStart, i);
									Out+=i;
									NextCodec->Len+=i;
									FlushOutStream(7);

									Out[0]='"';
									Out[1]='>';
									Out+=2;
									NextCodec->Len+=2;

									i=j;
								}
							}
							continue;
						}

						case ':': {
							if (j>2 && ((BlockStart+i+2)<BlockEnd)) {
								if ((BlockStart[i+1]=='/') && (BlockStart[i+2])=='/') {
#if 0
									if (!Codec->URL) {
										i=j;
										break;
									}
#endif

									Codec->State=LINK_URL;
									i=strlen(Codec->URL);
									FlushOutStream(25+i);			/* 23+safety+i */

									memcpy(Out, "<A TARGET=\"__eb\" HREF=\"", 23);
									Out+=23;
									NextCodec->Len+=23;

									memcpy(Out, Codec->URL, i);
									Out+=i;
									NextCodec->Len+=i;

									i=BlockEnd-BlockStart;

									FlushOutStream(i);

									memcpy(Out, BlockStart, i);
									Out+=i;
									NextCodec->Len+=i;
									FlushOutStream(7);

									Out[0]='"';
									Out[1]='>';
									Out+=2;
									NextCodec->Len+=2;

									i=j;
								}
							}
							continue;
						}
					}
				}
			}
		}

		FlushOutStream(6);

		switch(In[0]) {
			case '"': {
				memcpy(Out, "&quot;", 6);
				In++;
				Len++;
				Out+=6;
				NextCodec->Len+=6;
				continue;
			}

			case '<': {
				memcpy(Out, "&lt;", 4);
				In++;
				Len++;
				Out+=4;
				NextCodec->Len+=4;
				continue;
			}

			case '>': {
				memcpy(Out, "&gt;", 4);
				In++;
				Len++;
				Out+=4;
				NextCodec->Len+=4;
				continue;
			}

			case '&': {
				memcpy(Out, "&amp;", 5);
				In++;
				Len++;
				Out+=5;
				NextCodec->Len+=5;
				continue;
			}

			case 0x0d: {	/* CR */
				In++;
				Len++;
				continue;
			}

			case 0x0a: {	/* LF */
				memcpy(Out, "<BR>", 4);
				In++;
				Len++;
				Out+=4;
				NextCodec->Len+=4;
				continue;
			}

			default: {
				Out[0]=In[0];
				Out++;
				In++;
				NextCodec->Len++;
				Len++;
				continue;
			}
		}
	}

	if ((NextCodec->Len > 0) || (Codec->EOS)) {
	    unsigned long bytesProcessed;

	    if (Codec->EOS) {
		if (Codec->State > 0) {
		    if (Out + 4 >= OutEnd) {
			bytesProcessed = NextCodec->Codec(NextCodec, NextCodec->Next);
			
			if (bytesProcessed >= NextCodec->Len) {
			    NextCodec->Len = 0;
			    Out = NextCodec->Start;
			} else {
			    NextCodec->Len -= bytesProcessed;			
			    memmove(NextCodec->Start, Out-NextCodec->Len, NextCodec->Len);
			    Out = NextCodec->Start + NextCodec->Len;
			}
		    }
		    Codec->State = 0;
		    memcpy(Out, "</A>", 4);
		    Out += 4;
		    NextCodec->Len += 4;
		}

		if (Len == Codec->Len) {
		    NextCodec->EOS = TRUE;
		}
	    }

	    bytesProcessed = NextCodec->Codec(NextCodec, NextCodec->Next);
	    if (bytesProcessed >= NextCodec->Len) {
		NextCodec->Len = 0;
	    } else {
		NextCodec->Len -= bytesProcessed;
		memmove(NextCodec->Start, Out-NextCodec->Len, NextCodec->Len);
	    }
	}
        if (Len >= Codec->Min) {
            return(Len);
        }

        return(Codec->Len);
}

#define CODEC_HTML_TEXT_SUPRESS_CONTENT                               0
#define CODEC_HTML_TEXT_SUPRESS_TAG_EXAMINE                           1
#define CODEC_HTML_TEXT_SUPRESS_TAG                                   2
#define CODEC_HTML_TEXT_SUPRESS_META                                  3
#define CODEC_HTML_TEXT_SUPRESS_META_CHARSET                          4
#define CODEC_HTML_TEXT_SUPRESS_META_CHARSET_PRE_EQUAL                5
#define CODEC_HTML_TEXT_SUPRESS_META_CHARSET_PRE_VALUE                6
#define CODEC_HTML_TEXT_SUPRESS_META_CHARSET_VALUE                    7

#define CODEC_HTML_TEXT_SEND_CONTENT                                  8
#define CODEC_HTML_TEXT_SEND_TAG_EXAMINE                              9

#define CODEC_HTML_TEXT_SKIP_TAG                                     10


static int
HTML_Text_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
    unsigned char *In = Codec->Start;
    unsigned char *Out = NextCodec->Start + NextCodec->Len;
    unsigned char *OutEnd = NextCodec->End;
    unsigned char *End = In + Codec->Len;
    unsigned long  Len = 0;
    unsigned char *closeTagChar= NULL;
    FILE *debugHandle = NULL;

    OpenDebugLog(debugHandle, HTML_CODEC_DEBUG_FILE_PATH);
    do {
        ChangeStateDebugLog(debugHandle, StateString[Codec->State]);
        switch(Codec->State) {

        case CODEC_HTML_TEXT_SUPRESS_CONTENT: {
            /* skip everything up to the next tag */
            do {
                if (Len < Codec->Len) {
                    if (In[0] != '<') {
                        CodecSkipChar(&In, &Len, debugHandle);
                        continue;
                    }
                    Codec->State = CODEC_HTML_TEXT_SUPRESS_TAG_EXAMINE;	
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_TEXT_SUPRESS_TAG_EXAMINE: {
            /* lookup tag to see if it is the end tag. */
            /* if it is stop suppressing content */

            closeTagChar = In + 1;

            do {
                if (closeTagChar < End) {
                    if (*closeTagChar != '>') {
                        closeTagChar++;
                        continue;
                    }
                    break;
                }
                closeTagChar = NULL;
                break;
            } while (TRUE);

            if (closeTagChar || Codec->EOS || ((Codec->Len - Len) > strlen("<BODY"))) {
                if ((Codec->Len - Len) > strlen("<BODY")) {
                    if ((toupper(*(In + 1)) == 'M') && (XplStrNCaseCmp(In, "<meta ", strlen("<meta ")) == 0)) {
                        Codec->State = CODEC_HTML_TEXT_SUPRESS_META;
                        continue;
                    } else if ((toupper(*(In + 1)) == 'B') && 
                               ((XplStrNCaseCmp(In, "<body>", strlen("<body>")) == 0) || (XplStrNCaseCmp(In, "<body ", strlen("<body ")) == 0))) {
                        Codec->State = CODEC_HTML_TEXT_SKIP_TAG;
                        continue;
                    }
                }

                Codec->State = CODEC_HTML_TEXT_SUPRESS_TAG;
                continue;
            }

            /* Need more data */
            if ((Len > 0) || (Codec->End > (Codec->Start + Codec->Len))) {
                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            }

            /* 
               If we get here, we didn't have stuff to flush 
               and our buffer is too small to contain the whole tag.
            */
				
            Codec->State = CODEC_HTML_TEXT_SUPRESS_TAG;	
            continue;
        }

        case CODEC_HTML_TEXT_SUPRESS_TAG: {
            /* skip the current tag and stay in suppressed mode */
            do {
                if (Len < Codec->Len) {
                    if (In[0] != '>') {
                        CodecSkipChar(&In, &Len, debugHandle);
                        continue;
                    }
                    CodecSkipChar(&In, &Len, debugHandle);

                    Codec->State = CODEC_HTML_TEXT_SUPRESS_CONTENT;	
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_TEXT_SUPRESS_META: {
            /* We need to know the charset so that we can translate the content into utf-8 */
            if (Codec->Charset) {
                do {
                    if (Len < Codec->Len) {
                        if (*In != '>') {
                            if (tolower(*In) != 'c') {
                                CodecSkipChar(&In, &Len, debugHandle);
                                continue;
                            }

                            Codec->State = CODEC_HTML_TEXT_SUPRESS_META_CHARSET;
                            break;
                        }

                        CodecSkipChar(&In, &Len, debugHandle);
                        Codec->State = CODEC_HTML_TEXT_SUPRESS_CONTENT;
                        break;
                    }

                    HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                    return(Len);
                } while(TRUE);
                continue;
            }

            /* if there is not a Charset Codec we can't do anything even if we knew the charset */
            Codec->State = CODEC_HTML_TEXT_SUPRESS_TAG;
            continue;
        }

        case CODEC_HTML_TEXT_SUPRESS_META_CHARSET: {
            /* determine if the curent stream content is 'charset' */
            if (End > (In + sizeof("charset=\"\"") - 1)) {
                if (XplStrNCaseCmp(In, "charset", sizeof("charset") - 1) == 0) {
                    if (In[sizeof("charset") - 1] == '=') {
                        if (In[sizeof("charset=") - 1] != '>') {
                            if (In[sizeof("charset=") - 1] != '\'') {
                                if (In[sizeof("charset=") - 1] != '"') {
                                    if (!isspace(In[sizeof("charset=") - 1])) {
                                        CodecSkipChars(sizeof("charset=") - 1, &In, &Len, debugHandle);
                                        Codec->State = CODEC_HTML_TEXT_SUPRESS_META_CHARSET_VALUE;
                                        continue;
                                    }

                                    CodecSkipChars(sizeof("charset= ") - 1, &In, &Len, debugHandle);
                                    Codec->State = CODEC_HTML_TEXT_SUPRESS_META_CHARSET_PRE_VALUE;
                                    continue;
                                }
                            }

                            CodecSkipChars(sizeof("charset=\'") - 1, &In, &Len, debugHandle);
                            Codec->State = CODEC_HTML_TEXT_SUPRESS_META_CHARSET_VALUE;
                            continue;
                        }

                        CodecSkipChars(sizeof("charset=>") - 1, &In, &Len, debugHandle);
                        Codec->State = CODEC_HTML_TEXT_SUPRESS_CONTENT;
                        continue;
                    }
						
                    if (isspace(In[sizeof("charset") - 1])) {
                        CodecSkipChars(sizeof("charset ") - 1, &In, &Len, debugHandle);
                        Codec->State = CODEC_HTML_TEXT_SUPRESS_META_CHARSET_PRE_EQUAL;
                        continue;
                    }
                }

                CodecSkipChar(&In, &Len, debugHandle);
                Codec->State = CODEC_HTML_TEXT_SUPRESS_META;
                continue;
            }

            /* Need more data */
            if (!Codec->EOS) {
                return(Len);
            }

            Codec->State = CODEC_HTML_TEXT_SUPRESS_TAG;
            continue;
        }

        case CODEC_HTML_TEXT_SUPRESS_META_CHARSET_PRE_EQUAL: {
            /* supress white space up through the equal */
            do {
                if (Len < Codec->Len) {
                    CodecSkipChar(&In, &Len, debugHandle);
                    if (In[-1] == '=') {
                        Codec->State = CODEC_HTML_TEXT_SUPRESS_META_CHARSET_PRE_VALUE;
                        break;
                    }

                    if (isspace(In[-1])) {
                        continue;
                    }

                    if (In[-1] == '>') {
                        Codec->State = CODEC_HTML_TEXT_SUPRESS_CONTENT;
                        break;
                    }

                    Codec->State = CODEC_HTML_TEXT_SUPRESS_TAG;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_TEXT_SUPRESS_META_CHARSET_PRE_VALUE: {
            /* supress white space after equal */
            do {
                if (Len < Codec->Len) {
                    if (*In != '>') {
                        if (!isspace(*In)) {
                            Codec->State = CODEC_HTML_TEXT_SUPRESS_META_CHARSET_VALUE;
                            break;
                        }

                        CodecSkipChar(&In, &Len, debugHandle);
                        continue;
                    }

                    CodecSkipChar(&In, &Len, debugHandle);
                    Codec->State = CODEC_HTML_TEXT_SUPRESS_CONTENT;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_TEXT_SUPRESS_META_CHARSET_VALUE: {
            /* get the entire charset value in the buffer and look up the appropriate codec */
            unsigned char *endValue;

            endValue = In;

            do {
                if (endValue < End) {
                    if (*endValue != '"') {
                        if (*endValue != '\'') {
                            if (*endValue != '>') {
                                if (!isspace(*endValue)) {
                                    endValue++;
                                    continue;
                                }
                            }
                        }
                    }

                    break;
                }
                endValue = NULL;
                break;
            } while (TRUE);

            if (endValue) {
                unsigned char tmpChar;
                StreamCodecFunc	Func;
	
                tmpChar = *endValue;
                *endValue = '\0';
                Func = FindCodec(In, FALSE);
                *endValue = tmpChar;
                if (Func) {
                    /* the existence of Codec->Charset is ensured in CODEC_HTML_TEXT_SUPRESS_META */
                    ((StreamStruct *)(Codec->Charset))->Codec = Func;
                }

                CodecSkipChars(endValue - In, &In, &Len, debugHandle);
                Codec->State = CODEC_HTML_TEXT_SUPRESS_TAG;
                continue;
            }

            if ((!Codec->EOS) && ((End - In) < MAX_CHARSET_SIZE)) {
                /* go get more */
                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            }

            /* If we get here, the chaset value is ill formed or bigger than any we know about. */
            Codec->State = CODEC_HTML_TEXT_SUPRESS_TAG;
            continue;
        }

        case CODEC_HTML_TEXT_SEND_CONTENT: {
            /* Pass through the content between tags */
            do {
                if (Len < Codec->Len) {
                    if (In[0] != '<') {
                        HtmlDecodeFlushOutStream(1, &Out, OutEnd, Len, Codec, NextCodec);
                        CodecCopyChar(&In, &Out, &Len, NextCodec, debugHandle);
                        continue;
                    }
                    Codec->State = CODEC_HTML_TEXT_SEND_TAG_EXAMINE;	
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        case CODEC_HTML_TEXT_SEND_TAG_EXAMINE: {
            /* Look up the current tag and change state if appropriate */

            closeTagChar = In + 1;

            do {
                if (closeTagChar < End) {
                    if (*closeTagChar != '>') {
                        closeTagChar++;
                        continue;
                    }

                    closeTagChar++;

                    break;
                }
                closeTagChar = NULL;
                break;
            } while (TRUE);

            if (closeTagChar || Codec->EOS || ((Codec->Len - Len) > strlen("</BODY"))) {

                if ((Codec->Len - Len) > strlen("</BODY")) {
                    if ((toupper(*(In + 1)) == 'B') && 
                               (XplStrNCaseCmp(In, "</body>", strlen("</body>")) == 0)) {
                        Codec->State = CODEC_HTML_TEXT_SUPRESS_TAG;
                        continue;
                    }
                }
                Codec->State = CODEC_HTML_TEXT_SKIP_TAG;
                continue;
            }

            /* Need more data */
            if ((Len > 0) || (Codec->End > (Codec->Start + Codec->Len))) {
                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            }

            /* 
               If we get here, we didn't have stuff to flush 
               and our buffer is too small to contain the whole tag.
            */
				
            Codec->State = CODEC_HTML_TEXT_SKIP_TAG;	
            continue;
        }

        case CODEC_HTML_TEXT_SKIP_TAG: {
            /* skip the rest of a double quoted attribute value */
            do {
                if (Len < Codec->Len) {
                    if (In[0] != '>') {
                        CodecSkipChar(&In, &Len, debugHandle);
                        continue;
                    }

                    CodecSkipChar(&In, &Len, debugHandle);
                    Codec->State = CODEC_HTML_TEXT_SEND_CONTENT;
                    break;
                }

                HtmlDecodeFlushNext(Codec, NextCodec, Len, Out, debugHandle);
                return(Len);
            } while(TRUE);
            continue;
        }

        }
    } while(TRUE);
}
