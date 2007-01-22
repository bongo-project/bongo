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

#include "mime.h"
#include "messages.h"

#include <rfc2231.h>

#include <xpl.h>
#include <connio.h>

static BOOL
ParseDispositionParameters(unsigned char *ptr, MIMEPartStruct *part)
{
    RFC2231ParamStruct *rfc2231Name = NULL;

    do {
        while (isspace(*ptr)) {
            ptr++;
        }

        if (tolower(*ptr) == 'f' && XplStrNCaseCmp(ptr, "filename", sizeof("filename") - 1) == 0) {
            unsigned long nameLen = MIME_NAME_LEN;

            ptr = ParseParam(ptr + sizeof("filename") - 1, part->name, &nameLen, &rfc2231Name);
        }                                                

        ptr = strchr(ptr, ';');
        if ((ptr) && (*(ptr + 1) != '\0')) {
            ptr++;
            continue;
        }

        break;
    } while (TRUE);


    if (rfc2231Name == NULL) {
        ;
    } else {
        unsigned long NameLen = MIME_NAME_LEN;

        rfc2231JoinParamLines(part->name, &NameLen, rfc2231Name);
        rfc2231FreeParamLines(rfc2231Name);
    }
    return(TRUE);
}

__inline static BOOL
ParseTypeParameters(unsigned char *ptr, MIMEPartStruct *part)
{
    RFC2231ParamStruct *rfc2231Name = NULL;

    do {
        while (isspace(*ptr)) {
            ptr++;
        }

        if (tolower(*ptr) == 'b' && XplStrNCaseCmp(ptr, "boundary", 8) == 0) {
            unsigned long separatorLen = MIME_SEPARATOR_LEN;

            ptr = ParseParam(ptr + 8, part->separator, &separatorLen, NULL);
            part->sepLen = separatorLen;

        } else if (tolower(*ptr) == 'c' && XplStrNCaseCmp(ptr, "charset", 7) == 0) {
            unsigned long charsetLen = MIME_CHARSET_LEN;

            ptr = ParseParam(ptr + 7, part->charset, &charsetLen, NULL);

        } else if (tolower(*ptr) == 'n' && XplStrNCaseCmp(ptr, "name", 4) == 0) {
            unsigned long nameLen = MIME_NAME_LEN;

            ptr = ParseParam(ptr + 4, part->name, &nameLen, &rfc2231Name);
        }                                                

        ptr = strchr(ptr, ';');
        if ((ptr) && (*(ptr + 1) != '\0')) {
            ptr++;
            continue;
        }

        break;
    } while (TRUE);


    if (rfc2231Name == NULL) {
        ;
    } else {
        unsigned long NameLen = MIME_NAME_LEN;

        rfc2231JoinParamLines(part->name, &NameLen, rfc2231Name);
        rfc2231FreeParamLines(rfc2231Name);
    }
    return(TRUE);
}

__inline static int
SendMultiPartEnd(Connection *conn, MIMEPartStruct *part, unsigned long current)
{
    int j;
    long ccode;

    part[current].tmpMultiMark = part[current].multiMark;
    part[current].tmpEnd = part[current].end;
    part[current].tmpRFC822Mark = part[current].rfc822Mark;
    part[current].tmpRFC822End = part[current].rfc822End;

    /* We must preserve the right order, so it's a little complicated */
    for (j = current; j >= 0; j--) {
        if (part[current].tmpEnd && part[j].tmpMultiMark) {
            part[j].tmpMultiMark = FALSE;
            ccode = ConnWrite(conn, MSG2003MULTIEND, sizeof(MSG2003MULTIEND) - 1);
            if (ccode != -1) {
                part[current].tmpEnd--;
            } else {
                return(ccode);
            }
        }

        if (part[current].tmpRFC822End && part[j].tmpRFC822Mark) {
            part[j].tmpRFC822Mark = FALSE;
            ccode = ConnWrite(conn, MSG2004RFC822END, sizeof(MSG2004RFC822END) - 1);
            if (ccode != -1) {
                part[current].tmpRFC822End--;
            } else {
                return(ccode);
            }
        }
    }
    return(0);
}

int 
SendMIME(Connection *conn, MIMEReportStruct *report)
{
    int ccode;
    unsigned long i = 0;
    MIMEPartStruct *part;
    unsigned long parts;

    parts = report->parts;
    part = report->part;

    if (parts > 1) {
        ccode = 0;
        for (i=0; i < parts; i++) {
            /* 2002 <type> <subtype> <charset> <encoding> <name> <part-head-start> <part-head-size> <part-start-pos> <Size> <RFC822 Header-Size> <lines> */
            if (XplStrCaseCmp(part[i].subtype, "RFC822") && ((i + 1) < parts)) {
                ccode = ConnWriteF(conn, "2002-%s %s %s %s \"%s\" %lu %lu %lu %lu\r\n", 
                                   part[i].type, 
                                   part[i].subtype, 
                                   part[i].charset, 
                                   part[i].encoding, 
                                   part[i].name, 
                                   part[i].start, 
                                   part[i].length, 
                                   part[i + 1].start - part[i].start, 
                                   part[i].lines);
            } else if (XplStrCaseCmp(part[i].type, "TEXT")) {
                ccode = ConnWriteF(conn, "2002-%s %s %s %s \"%s\" %lu %lu 0 %lu\r\n", 
                                   part[i].type, 
                                   part[i].subtype, 
                                   part[i].charset, 
                                   part[i].encoding, 
                                   part[i].name, 
                                   part[i].start, 
                                   part[i].length, 
                                   part[i].lines);
            } else {
                ccode = ConnWriteF(conn, "2002-%s %s %s %s \"%s\" %lu %lu 0 0\r\n", 
                                   part[i].type, 
                                   part[i].subtype, 
                                   part[i].charset, 
                                   part[i].encoding, 
                                   part[i].name, 
                                   part[i].start, 
                                   part[i].length);
            }

            if (ccode != -1) {
                ccode = SendMultiPartEnd(conn, part, i);
            }
        }
    } else {
        ccode = ConnWriteF(conn, "2002-%s %s %s %s \"%s\" 0 %lu 0 %lu\r\n", part[i].type, part[i].subtype, part[i].charset, part[i].encoding, part[i].name, part[i].length, part[i].lines);
    }

    if (ccode != -1) {
        ConnWrite(conn, MSG1000OK, sizeof(MSG1000OK) - 1);
    }

    return(ccode);
}

int 
SendMimeDetails(Connection *conn, MIMEReportStruct *report)
{
    int ccode;
    unsigned long i = 0;
    MIMEPartStruct *part;
    unsigned long parts;

    parts = report->parts;
    part = report->part;

    if (parts > 1) {
        ccode = 0;
        for (i=0; i < parts; i++) {
            /* 2002 <type> <subtype> <charset> <encoding> <name> <part-head-start> <part-head-size> <part-start-pos> <Size> <RFC822 Header-Size> <lines> */
            if (XplStrCaseCmp(part[i].subtype, "RFC822") && ((i + 1) < parts)) {
                ccode = ConnWriteF(conn, "2002-%s %s %s %s \"%s\" %d %lu %lu %lu %lu %lu\r\n", 
                                   part[i].type, 
                                   part[i].subtype, 
                                   part[i].charset, 
                                   part[i].encoding, 
                                   part[i].name, 
                                   part[i].headerStart,
                                   part[i].headerLen,
                                   part[i].start, 
                                   part[i].length, 
                                   part[i + 1].start - part[i].start, 
                                   part[i].lines);
            } else if (XplStrCaseCmp(part[i].type, "TEXT")) {
                ccode = ConnWriteF(conn, "2002-%s %s %s %s \"%s\" %d %lu %lu %lu 0 %lu\r\n", 
                                   part[i].type, 
                                   part[i].subtype, 
                                   part[i].charset, 
                                   part[i].encoding, 
                                   part[i].name, 
                                   part[i].headerStart,
                                   part[i].headerLen,
                                   part[i].start, 
                                   part[i].length, 
                                   part[i].lines);
            } else {
                ccode = ConnWriteF(conn, "2002-%s %s %s %s \"%s\" %d %lu %lu %lu 0 0\r\n", 
                                   part[i].type, 
                                   part[i].subtype, 
                                   part[i].charset, 
                                   part[i].encoding, 
                                   part[i].name, 
                                   part[i].headerStart,
                                   part[i].headerLen,
                                   part[i].start, 
                                   part[i].length);
            }

            if (ccode != -1) {
                ccode = SendMultiPartEnd(conn, part, i);
            }
        }
    } else {
        ccode = ConnWriteF(conn, "2002-%s %s %s %s \"%s\" 0 0 0 %lu 0 %lu\r\n", part[i].type, part[i].subtype, part[i].charset, part[i].encoding, part[i].name, part[i].length, part[i].lines);
    }

    if (ccode != -1) {
        ConnWrite(conn, MSG1000OK, sizeof(MSG1000OK) - 1);
    }

    return(ccode);
}

void 
FreeMIME(MIMEReportStruct *report)
{
    if (report && report->part) {
        MemFree(report->part);
    }
}

MIMEReportStruct *
ParseMIME(FILE *fh, unsigned long headerSize, unsigned long messageSize, long bodyLines, unsigned char *line, unsigned long lineSize)
{
    int i;
    int j;
    int useThisPartNo = 0;
    int maxPartNo = 1;
    int partsAllocated = 0;
    int partNo = 0;
    int partNoM = 0;
    unsigned long fileOffset;
    unsigned long sPos = 0;
    unsigned long len = 0;
    unsigned long count;
    unsigned long lastHeaderStart = 0;
    unsigned long lastHeaderLen = 0;
    unsigned long contentHeaderLen = 0;
    unsigned char *ptr;
    unsigned char *start;
    unsigned char *end;
    unsigned char *header = NULL;
    unsigned char *currentSeparator = NULL;
    unsigned char *allocatedSeparator = NULL;
    BOOL lastInHeader = TRUE;
    BOOL inHeader = TRUE;
    BOOL skip = FALSE;
    BOOL doingDigest = FALSE;
    BOOL addDefaultHeader = FALSE;
    BOOL haveContent = FALSE;
    MIMEReportStruct *report = NULL;
    MIMEPartStruct *part = NULL;
    
    partsAllocated = MIME_STRUCT_ALLOC;
    part = (MIMEPartStruct *)MemMalloc((sizeof(MIMEPartStruct) * partsAllocated) + sizeof(MIMEReportStruct));
    header = (unsigned char *)MemMalloc(sizeof(unsigned char)*MAX_HEADER);
    if (!part || !header) {
        if (part) {
            MemFree(part);
        }
        if (header) {
            MemFree(header);
        }
        return(NULL);
    }    

    /* null out allocated memory */
    header[0]        = '\0';
    memset(part, 0, sizeof(MIMEPartStruct) * partsAllocated);

    /* initialize default part structure */
    strcpy(part[0].type, "text");
    strcpy(part[0].subtype, "plain");
    strcpy(part[0].charset, "US-ASCII");
    strcpy(part[0].encoding, "-");
    part[0].name[0]='\0';

    /* initialize locals from parameters */
    count            = messageSize;
    fileOffset    = ftell(fh);
    line[0]        = '\0';

    while (count>0) {
        do {
            /* Check to see if we've hit the end of the header. 'CR LF' is the only valid indicator */
            /* but this will handle common broken alternatives such as 'CR CR LF' and 'LF' */
            unsigned long i = 0;

            while (line[i] ==    '\r') {
                i++;
            }    
            if (line[i] == '\n') {
                lastHeaderLen = sPos - lastHeaderStart; 
                if (!skip) {
                    inHeader=FALSE;
                } else {
                    lastHeaderStart = sPos;
                    skip=FALSE;
                    useThisPartNo=partNo;
                    if (!part[partNoM].start) {
                        part[partNoM].headerStart = sPos - lastHeaderLen;
                        part[partNoM].headerLen = lastHeaderLen;
                        part[partNoM].start = sPos;
                    } else {
                        part[partNo].headerStart = sPos - lastHeaderLen;
                        part[partNo].headerLen = lastHeaderLen;
                        part[partNo].start = sPos;
                    }
                }
                line[0]=0;
            } else {
                header[0]=toupper(header[0]);
                if (header[0]=='C' && toupper(header[1])=='O') {
                    CHOP_NEWLINE(header);
                    ptr=line;
                    while (isspace(*ptr)) {
                        ptr++;
                    }
                    contentHeaderLen+=len;
                    if(contentHeaderLen<MAX_HEADER) {
                        strcat(header, ptr);
                    }    
                } else {
                    strcpy(header, line);
                    contentHeaderLen=len;
                }
                if (fgets(line, lineSize, fh)) {
                    len=strlen(line);
                    count-=len;
                    sPos+=len;
                } else {
                    if (allocatedSeparator) {
                        MemFree(allocatedSeparator);
                    }
                    MemFree(part);
                    MemFree(header);
                    return(NULL);
                }
            }
        } while (inHeader && isspace(line[0]));

        if (lastInHeader) {
            if (toupper(header[0])=='C' && header[7]=='-' && XplStrNCaseCmp(header + 1, "ontent", sizeof("ontent") - 1) == 0) {
                /* the header buffer contains a MIME header */
                if (!haveContent && XplStrNCaseCmp(header + sizeof("Content-") - 1, "type:", sizeof("type:") - 1) == 0) {
                    /* Content-Type */
                    ptr = strchr(header + 13, '/');
                    if (ptr) {
                        addDefaultHeader = FALSE;
                        haveContent = TRUE;
                        skip = FALSE;
                        part[partNo].type[0] = '-';
                        part[partNo].type[1] = '\0';
                        part[partNo].subtype[0] = '-';
                        part[partNo].subtype[1] = '\0';
                        part[partNo].charset[0] = '-';
                        part[partNo].charset[1] = '\0';
                        part[partNo].name[0] = '\0';
                        if (part[partNo].encoding[0] == '\0') {
                            part[partNo].encoding[0] = '-';
                            part[partNo].encoding[1] = '\0';
                        }
                        start=ptr;
                        end=ptr;
                        while ((start>(header + 13)) && !isspace(*(start - 1))) {
                            start--;
                        }
                        while (!isspace(*end) && (*end != ';')) {
                            end++;
                        }
                        *ptr = '\0';
                        *end = '\0';
                        strcpy(part[partNo].type, start);
                        strcpy(part[partNo].subtype, ptr + 1);
                        if (XplStrCaseCmp(part[partNo].subtype, "rfc822") == 0) {
                            part[partNo].rfc822Start = TRUE;
                            skip = TRUE;
                            addDefaultHeader = TRUE;

                            if (!inHeader) {
                                inHeader = TRUE;
                                if (!part[partNoM].start) {
                                    part[partNoM].headerStart = sPos - lastHeaderLen;
                                    part[partNoM].headerLen = lastHeaderLen;
                                    part[partNoM].start = sPos;
                                } else {
                                    part[partNo].headerStart = sPos - lastHeaderLen;
                                    part[partNo].headerLen = lastHeaderLen;
                                    part[partNo].start = sPos;
                                }
                            }
                        } else if (XplStrCaseCmp(part[partNo].subtype, "digest") == 0) {
                            doingDigest = TRUE;
                        }
                        ParseTypeParameters(end + 1, &part[partNo]);
                        if (part[partNo].separator) {
                            currentSeparator = part[partNo].separator;
                        }
                        partNo++;

                        if (partNo >= partsAllocated) {
                            void *Tmp;

                            if (currentSeparator) {
                                if (!allocatedSeparator) {
                                    allocatedSeparator = MemStrdup(currentSeparator);
                                    currentSeparator = allocatedSeparator;    
                                } else if (allocatedSeparator != currentSeparator) {
                                    MemFree(allocatedSeparator);
                                    allocatedSeparator = MemStrdup(currentSeparator);
                                    currentSeparator = allocatedSeparator;
                                }         
                            }
                        
                            Tmp = MemRealloc(part, (sizeof(MIMEPartStruct) * (partsAllocated + MIME_STRUCT_ALLOC)) + sizeof(MIMEReportStruct));
                            if (Tmp) {
                                part = (MIMEPartStruct *)Tmp;
                                memset((void *)((unsigned long)part + (unsigned long)((unsigned long)sizeof(MIMEPartStruct) * (unsigned long)partsAllocated)), 0, sizeof(MIMEPartStruct) * MIME_STRUCT_ALLOC);
                                partsAllocated += MIME_STRUCT_ALLOC;
                            } else {
                                if (allocatedSeparator) {
                                    MemFree(allocatedSeparator);
                                }
                                MemFree(part);
                                MemFree(header);
                                return(NULL);
                            }
                        }

                        if (partNo > 1) {
                            partNoM++;
                        }
                        useThisPartNo = partNoM;
                    }
                } else if (XplStrNCaseCmp(header + sizeof("Content-") - 1, "transfer-encoding:", sizeof("transfer-encoding:") - 1) == 0) {
                    /* Content-Transfer-Encoding */
                    register int    TempLen;

                    ptr = header + sizeof("Content-Transfer-Encoding:") - 1;
                    while (isspace(*ptr)) {
                        ptr++;
                    }
                    CHOP_NEWLINE(header);
                    TempLen = min(strlen(ptr), MIME_ENCODING_LEN);
                    memcpy(part[useThisPartNo].encoding, ptr, TempLen);
                    part[useThisPartNo].encoding[TempLen] = '\0';
                    ptr = part[useThisPartNo].encoding+TempLen - 1;
                    while (*ptr == ' ') {
                        *ptr = '\0';
                        ptr--;
                    }

                } else if (XplStrNCaseCmp(header + sizeof("Content-") - 1, "disposition:", sizeof("disposition:") - 1) == 0) {
                    /* Content-Disposition */
                    ptr = header + sizeof("Content-Disposition:") - 1;
                    do {
                        if (isspace(*ptr)) {
                            ptr++;
                            continue;
                        }

                        break;
                    } while(TRUE);

                    if (XplStrNCaseCmp(ptr, "attachment;", sizeof("attachment;") - 1) == 0) {
                        ParseDispositionParameters(ptr + sizeof("attachment;") - 1, &part[useThisPartNo]);
                    }
                }
            } else {
                if (addDefaultHeader && !inHeader && line[0]=='\0') {
                    if (partNo>0) {
                        if (doingDigest) {
                            strcpy(part[partNo].type, "message");
                            strcpy(part[partNo].subtype, "rfc822");
                            strcpy(part[partNo].charset, "US-Ascii");
                            strcpy(part[partNo].encoding, "7bit");
                            part[partNo].name[0]='\0';
                            part[partNo].rfc822Start=TRUE;
                            skip=TRUE;
                            if (!inHeader) {
                                if (!part[partNoM].start) {
                                    part[partNoM].headerStart = sPos - lastHeaderLen;
                                    part[partNoM].headerLen = lastHeaderLen;
                                    part[partNoM].start = sPos;
                                } else {
                                    part[partNo].headerStart = sPos - lastHeaderLen;
                                    part[partNo].headerLen = lastHeaderLen;
                                    part[partNo].start = sPos;
                                }
                            }
                            inHeader=TRUE;
                        } else {
                            strcpy(part[partNo].type, "text");
                            strcpy(part[partNo].subtype, "plain");
                            strcpy(part[partNo].charset, "US-ASCII");
                            strcpy(part[partNo].encoding, "7bit");
                            part[partNo].name[0]='\0';
                        }
                        partNo++;
                        if (partNo>=partsAllocated) {
                            void *Tmp;
                            if (currentSeparator) {
                                if (!allocatedSeparator) {
                                    allocatedSeparator = MemStrdup(currentSeparator);
                                    currentSeparator = allocatedSeparator;    
                                } else if (allocatedSeparator != currentSeparator) {
                                    MemFree(allocatedSeparator);
                                    allocatedSeparator = MemStrdup(currentSeparator);
                                    currentSeparator = allocatedSeparator;
                                }         
                            }
                            Tmp = MemRealloc(part, (sizeof(MIMEPartStruct) * (partsAllocated + MIME_STRUCT_ALLOC)) + sizeof(MIMEReportStruct));
                            if (Tmp) {
                                part = (MIMEPartStruct *)Tmp;
                                memset((void *)((unsigned long)part + (unsigned long)((unsigned long)sizeof(MIMEPartStruct) * (unsigned long)partsAllocated)), 0, sizeof(MIMEPartStruct) * MIME_STRUCT_ALLOC);
                                partsAllocated += MIME_STRUCT_ALLOC;
                            } else {
                                if (allocatedSeparator) {
                                    MemFree(allocatedSeparator);
                                }
                                MemFree(part);
                                MemFree(header);
                                return(NULL);
                            }
                        }
                        if (partNo>1) {
                            partNoM++;
                        }
                    }
                    addDefaultHeader=FALSE;
                }
            }

            if (partNo>maxPartNo) {
                maxPartNo=partNo;
            }
            if (line[0]=='\0') {
                haveContent=FALSE;
            }
        } else {          
            if (!part[partNoM].start) {
                part[partNoM].headerStart = sPos - len - lastHeaderLen;
                part[partNoM].headerLen = lastHeaderLen;
                part[partNoM].start = sPos - len;
            }
            if (line[0]=='-' && line[1]=='-') {
                /* Check for a separator */
                for (i=partNoM; i>=0; i--) {
                    if (part[i].separator[0]!='\0') {
                        if (strncmp(line+2, part[i].separator, part[i].sepLen)==0) {
                            if (currentSeparator && (strncmp(part[i].separator, currentSeparator, part[i].sepLen) != 0)) {
                                /* BROKEN MIME!!    We have reached an "outer boundry" of a parent MIME part with unterminated sub-parts */
                                /* Close all sub-parts that remain open */
                                for (j = partNoM; j > i; j--) {
                                    if ((part[j].separator[0] != '\0') && (XplStrNCaseCmp(part[j].separator, currentSeparator, part[j].sepLen)) && (!XplStrNCaseCmp(part[j].subtype, "rfc822", 6))) {
                                        long k;

                                        part[partNoM].end++;    
                                        part[j].multiMark = TRUE;
                                        part[j].length = sPos-len-part[j].start;
                                        inHeader=FALSE;
                                        currentSeparator = NULL;
                                        for (k = j - 1; k > i; k--) {
                                            if ((part[k].separator[0] != '\0') && (part[k].multiMark != TRUE)) {
                                                currentSeparator = part[k].separator;
                                                break; 
                                            }
                                        }
                                        if (currentSeparator == NULL) {
                                            currentSeparator = part[i].separator;
                                        }
                                    }
                                }
                            }
                            addDefaultHeader=TRUE;
                            if (!part[partNoM].length) {
                                part[partNoM].length=sPos-len-part[partNoM].start;
                            }

                            useThisPartNo=partNo;

                            for (j=i; j<partNoM; j++) {
                                if (part[j].rfc822Start) {
                                    part[j].rfc822Start=FALSE;
                                    part[j].rfc822Mark=TRUE;
                                    part[partNoM].rfc822End++;
                                }
                            }
                            
                            for (j=partNoM-1; j>i; j--) {
                                if (!part[j].length) {
                                    part[j].length=sPos-len-part[j].start;
                                }
                            }
                            if (line[part[i].sepLen+2]=='-') {
                                addDefaultHeader=FALSE;
                                part[partNoM].end++;
                                part[i].multiMark=TRUE;
                                part[i].length=sPos-len-part[i].start;
                                inHeader=FALSE;
                                currentSeparator = NULL;
                                for (j = i - 1; j >= 0; j--) {
                                    if ((part[j].separator[0] != '\0') && (part[j].multiMark != TRUE)) {
                                        currentSeparator = part[j].separator; 
                                        break;
                                    }
                                }
                            } else {
                                inHeader = TRUE;
                                lastHeaderStart = sPos;
                            }
                            break;
                        }
                    }
                }
            }
        }

        header[0]='\0';
        lastInHeader=inHeader;
    }

    if (currentSeparator != NULL) {
        /* BROKEN MIME!!    We reached the end of the message and not all parts are terminated */
        /* Close all parts that remain open */
        for (j = maxPartNo - 1; j >= 0; j--) {
            if ((part[j].separator[0] != '\0') && (XplStrNCaseCmp(part[j].separator, currentSeparator, part[j].sepLen)) && (!XplStrNCaseCmp(part[j].subtype, "rfc822", 6))) {
                long k;
                
                part[maxPartNo - 1].end++;    
                part[j].multiMark = TRUE;
                part[j].length = sPos - len - part[j].start;
                currentSeparator = NULL;
                for (k = j - 1; k > 0; k--) {
                    if ((part[k].separator[0] != '\0') && (part[k].multiMark != TRUE)) {
                        currentSeparator = part[k].separator;
                        break; 
                    }
                }
                if (currentSeparator == NULL) {
                    break;
                }
            }
        }
    }

    if (maxPartNo>1) {

        for (j=maxPartNo-1; j>=0; j--) {
            if (part[j].rfc822Start) {
                part[j].rfc822Start=FALSE;
                part[j].rfc822Mark=TRUE;
                part[maxPartNo-1].rfc822End++;
                break;
            }
        }
        for (j=maxPartNo-1; j>=0; j--) {
            if (!part[j].length) {
                part[j].length=sPos-len-part[j].start;
            }
        }
        for (i=0; i < maxPartNo; i++) {

            if (XplStrCaseCmp(part[i].subtype, "RFC822") && ((i+1)<maxPartNo)) {
                fseek(fh, part[i].start + fileOffset, SEEK_SET);
                count=part[i].length;
                do {
                    if (fgets(line, lineSize, fh)) {
                        part[i].lines++;
                        count-=strlen(line);
                    }
                } while (!feof(fh) && !ferror(fh) && count>0);
            } else if (XplStrCaseCmp(part[i].type, "TEXT")) {
                fseek(fh, part[i].start + fileOffset, SEEK_SET);
                count=part[i].length;
                do {
                    if (fgets(line, lineSize, fh)) {
                        part[i].lines++;
                        count-=strlen(line);
                    }
                } while (!feof(fh) && !ferror(fh) && count>0);
            }

            part[i].start -= headerSize;
            part[i].headerStart -= headerSize;

        }
    } else {
        if (bodyLines == -1) {
            /* The number of body lines is not known */
            fseek(fh, headerSize, SEEK_SET);
            bodyLines = 0;
            while (!feof(fh) && !ferror(fh)) {
                if (fgets(line, lineSize, fh)!=NULL) {
                    bodyLines++;
                }
            }
        }
        part[0].length = messageSize - headerSize;
        part[0].lines = bodyLines;
    }

    if (allocatedSeparator) {
        MemFree(allocatedSeparator);
    }
    MemFree(header);

    report = (MIMEReportStruct *)(part + maxPartNo);

    report->parts = maxPartNo;
    report->part    = part;
    return(report);
}
