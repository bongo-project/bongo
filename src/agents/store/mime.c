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
#include <rfc2231.h>

#include "stored.h"
#include "messages.h"
#include "mime.h"

__inline static void
MimeReportFixupInternal(MimeReport *report)
{
    unsigned long i;
    long offset;
    MimeResponseLine *line;

    if ((char *)report == report->base) {
        return;
    }

    offset = (char *)report - report->base;
    report->line = (MimeResponseLine *)((char *)report->line + offset);
    line = &(report->line[0]);
    for (i = 0; i < report->lineCount; i++) {
        if (line->code == 2002) {
            line->type += offset;
            line->subtype += offset;
            line->charset += offset;
            line->encoding += offset;
            line->name += offset;
        }
        line++;
    }
    report->base = (char *)report;
}

void
MimeReportFixup(MimeReport *report)
{
    MimeReportFixupInternal(report);
}

__inline static char *
MimeCopyString(char *partString, char **nextString)
{
    unsigned long len;
    char *copy;

    copy = *nextString;
    len = strlen(partString);
    memcpy(copy, partString, len);
    copy[len] = '\0';
    *nextString += (len + 1);
    return(copy);
}

__inline static MimeReport *
MimeReportCreate(MIMEPartStruct *part, unsigned long partCount)
{
    int j;
    unsigned long currentPart;
    unsigned long currentLine;
    unsigned long reportLen;
    unsigned long lineCount;
    unsigned long totalStringLen;
    char *nextString;
    MimeReport *internalReport;

    /* count the number of lines and the total length of strings */
    totalStringLen = 0;
    lineCount = partCount;

    for (currentPart = 0; currentPart < partCount; currentPart++) {
        if (part[currentPart].multiMark) {
            lineCount++;
        }
        if (part[currentPart].rfc822Mark) {
            lineCount++;
        }
        totalStringLen += (strlen(part[currentPart].type) + 1);
        totalStringLen += (strlen(part[currentPart].subtype) + 1);
        totalStringLen += (strlen(part[currentPart].charset) + 1);
        totalStringLen += (strlen(part[currentPart].encoding) + 1);
        totalStringLen += (strlen(part[currentPart].name) + 1);
    }

    reportLen = sizeof(MimeReport) + (lineCount * sizeof(MimeResponseLine)) + totalStringLen;
    internalReport = MemMalloc(reportLen);
    if (!internalReport) {
        return(NULL);
    }

    internalReport->base = (char *)internalReport;
    internalReport->size = reportLen;
    internalReport->partCount = partCount;
    internalReport->lineCount = lineCount;
    internalReport->line = (MimeResponseLine *)((char *)internalReport + sizeof(MimeReport));
    nextString = (char *)internalReport + sizeof(MimeReport) + (lineCount * sizeof(MimeResponseLine));
    currentLine = 0;
    for (currentPart = 0; currentPart < partCount; currentPart++) {
        /* 2002 <type> <subtype> <charset> <encoding> <name> <part-head-start> <part-head-size> <part-start-pos> <Size> <RFC822 Header-Size> <lines> */
        if (currentLine < lineCount) {
            internalReport->line[currentLine].code = 2002;

            internalReport->line[currentLine].type = MimeCopyString(part[currentPart].type, &nextString);
            internalReport->line[currentLine].subtype = MimeCopyString(part[currentPart].subtype, &nextString);
            internalReport->line[currentLine].charset = MimeCopyString(part[currentPart].charset, &nextString);
            internalReport->line[currentLine].encoding = MimeCopyString(part[currentPart].encoding, &nextString);
            internalReport->line[currentLine].name = MimeCopyString(part[currentPart].name, &nextString);

            internalReport->line[currentLine].headerStart = part[currentPart].headerStart;
            internalReport->line[currentLine].headerLen = part[currentPart].headerLen;
            internalReport->line[currentLine].start = part[currentPart].start;
            internalReport->line[currentLine].len = part[currentPart].length;

            if (QuickCmp(part[currentPart].subtype, "RFC822") && ((currentPart + 1) < partCount)) {
                internalReport->line[currentLine].messageHeaderLen = part[currentPart + 1].start - part[currentPart].start;
                internalReport->line[currentLine].lines = part[currentPart].lines;
            } else {
                internalReport->line[currentLine].messageHeaderLen = 0;
                if (QuickCmp(part[currentPart].type, "TEXT")) {
                    internalReport->line[currentLine].lines = part[currentPart].lines;
                } else {
                    internalReport->line[currentLine].lines = 0;
                }
            }

            currentLine++;

            /* We must preserve the right order, so it's a little complicated */
            for (j = currentPart; j >= 0; j--) {
                if ((part[currentPart].end && part[j].multiMark) && (currentLine < lineCount)) {
                    part[j].multiMark = FALSE;
                    part[currentPart].end--;
                    internalReport->line[currentLine].code = 2003;
                    currentLine++;
                }

                if ((part[currentPart].rfc822End && part[j].rfc822Mark) && (currentLine < lineCount)) {
                    part[j].rfc822Mark = FALSE;
                    part[currentPart].rfc822End--;
                    internalReport->line[currentLine].code = 2004;
                    currentLine++;
                }
            }
        }
    }
    return(internalReport);
}

CCode
MimeReportSend(void *param, MimeReport *report)
{
    StoreClient *client = (StoreClient *)param;
    CCode ccode = 0;
    unsigned long count = 0;
    MimeResponseLine *line;
    
    MimeReportFixupInternal(report);

    line = &(report->line[0]);
    for (;;) {
        if (line->code == 2002) {
            ccode = ConnWriteF(client->conn, "2002 %s %s %s %s \"%s\" %lu %lu %lu %lu %lu %lu\r\n", 
                               line->type, 
                               line->subtype, 
                               line->charset, 
                               line->encoding, 
                               line->name, 
                               line->headerStart,
                               line->headerLen,
                               line->start, 
                               line->len, 
                               line->messageHeaderLen,
                               line->lines);
        } else if (line->code == 2003) {
            ccode = ConnWrite(client->conn, MSG2003MULTIEND, strlen(MSG2003MULTIEND));
        } else {
            ccode = ConnWrite(client->conn, MSG2004RFC822END, strlen(MSG2004RFC822END));
        } 

        if (ccode != -1) {
            count++;
            if (count < report->lineCount) {
                line++;
                continue;
            }
            return ccode;
        }
        break;
    }
    return ccode;
}

void
MimeReportFree(MimeReport *report)
{
    if (report) {
        MemFree(report);
    }
}


__inline static BOOL
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

__inline static long
FileGetLine(FILE *fh, char *buffer, unsigned long bufferLen, unsigned long offset)
{
    unsigned long len;

    if (fgets(buffer, bufferLen, fh)) {
        len = strlen(buffer);
        if (buffer[len - 1] == '\n') {
            return(len);
        }
        return(ftell(fh) - offset);
    }
    return(0);
}

MimeReport *
MimeParse(FILE *fh, size_t messageSize, unsigned char *line, unsigned long lineSize)
{
    MimeReport *report;
    int i;
    int j;
    int useThisPartNo = 0;
    int maxPartNo = 1;
    int partsAllocated = 0;
    int partNo = 0;
    int partNoM = 0;
    int workPartNo = 0;
    int workPartNoM = 0;
    BOOL haveWorkPart = FALSE;
    size_t fileOffset;
    size_t sPos = 0;
    size_t len = 0;
    long count;
    size_t lastHeaderStart = 0;
    size_t lastHeaderLen = 0;
    size_t contentHeaderLen = 0;
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
    MIMEPartStruct *part = NULL;
    
    partsAllocated = MIME_STRUCT_ALLOC;
    part = (MIMEPartStruct *)MemMalloc((sizeof(MIMEPartStruct) * partsAllocated));
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

    while (count > 0) {
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

                len = FileGetLine(fh, line, lineSize, fileOffset + sPos);
                if (len) {
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
                if (!haveWorkPart) {
                    haveWorkPart = TRUE;
                    addDefaultHeader = FALSE;
                    skip = FALSE;
                    
                    workPartNo = partNo;
                    workPartNoM = partNoM;

                    strcpy(part[partNo].type, "text");
                    strcpy(part[partNo].subtype, "plain");
                    strcpy(part[partNo].charset, "us-ascii");
                    part[partNo].encoding[0] = '-';
                    part[partNo].encoding[1] = '\0';
                    part[partNo].name[0] = '\0';

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
                        
                        Tmp = MemRealloc(part, (sizeof(MIMEPartStruct) * (partsAllocated + MIME_STRUCT_ALLOC)));
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

                /* the header buffer contains a MIME header */
                if (XplStrNCaseCmp(header + sizeof("Content-") - 1, "type:", sizeof("type:") - 1) == 0) {
                    /* Content-Type */
                    ptr = strchr(header + 13, '/');
                    if (ptr) {
                        start = ptr;
                        end = ptr;
                        while ((start > (header + 13)) && !isspace(*(start - 1))) {
                            start--;
                        }
                        while (!isspace(*end) && (*end != ';')) {
                            end++;
                        }
                        *ptr = '\0';
                        *end = '\0';
                        strcpy(part[workPartNo].type, start);
                        strcpy(part[workPartNo].subtype, ptr + 1);
                        if (XplStrCaseCmp(part[workPartNo].subtype, "rfc822") == 0) {
                            part[workPartNo].rfc822Start = TRUE;
                            skip = TRUE;
                            addDefaultHeader = TRUE;

                            if (!inHeader) {
                                inHeader = TRUE;
                                if (!part[workPartNoM].start) {
                                    part[workPartNoM].headerStart = sPos - lastHeaderLen;
                                    part[workPartNoM].headerLen = lastHeaderLen;
                                    part[workPartNoM].start = sPos;
                                } else {
                                    part[workPartNo].headerStart = sPos - lastHeaderLen;
                                    part[workPartNo].headerLen = lastHeaderLen;
                                    part[workPartNo].start = sPos;
                                }
                            }
                        } else if (XplStrCaseCmp(part[workPartNo].subtype, "digest") == 0) {
                            doingDigest = TRUE;
                        }
                        ParseTypeParameters(end + 1, &part[workPartNo]);
                        if (part[workPartNo].separator) {
                            currentSeparator = part[workPartNo].separator;
                        }
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
                            Tmp = MemRealloc(part, (sizeof(MIMEPartStruct) * (partsAllocated + MIME_STRUCT_ALLOC)));
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
                haveWorkPart = FALSE;
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
                                    if ((part[j].separator[0] != '\0') && (QuickNCmp(part[j].separator, currentSeparator, part[j].sepLen)) && (!QuickNCmp(part[j].subtype, "rfc822", 6))) {
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
            if ((part[j].separator[0] != '\0') && (QuickNCmp(part[j].separator, currentSeparator, part[j].sepLen)) && (!QuickNCmp(part[j].subtype, "rfc822", 6))) {
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
        unsigned long readLen;
        unsigned long partEndOffset;

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

            if (QuickCmp(part[i].subtype, "RFC822") && ((i+1)<maxPartNo)) {
                XplFSeek64(fh, part[i].start + fileOffset, SEEK_SET);
                count=part[i].length;
                partEndOffset = fileOffset + part[i].start + part[i].length;
                do {
                    readLen = FileGetLine(fh, line, lineSize, partEndOffset - count);
                    part[i].lines++;
                    count -= readLen;
                } while (!feof(fh) && !ferror(fh) && count>0);
            } else if (QuickCmp(part[i].type, "TEXT")) {
                XplFSeek64(fh, part[i].start + fileOffset, SEEK_SET);
                count = part[i].length;
                partEndOffset = fileOffset + part[i].start + part[i].length;
                do {
                    readLen = FileGetLine(fh, line, lineSize, partEndOffset - count);
                    part[i].lines++;
                    count -= readLen;
                } while (!feof(fh) && !ferror(fh) && count>0);
            }
        }
    } else {
        unsigned long readLen;
        unsigned long partEndOffset;

        /* The number of body lines is not known */
        if (part[0].headerLen > 0) {
            part[0].start = part[0].headerLen;

            XplFSeek64(fh, fileOffset + part[0].headerLen, SEEK_SET);
            count = messageSize - part[0].headerLen;
            partEndOffset = fileOffset + messageSize;
            part[0].length = count;
            part[0].lines = 0;
            while (!feof(fh) && !ferror(fh) && (count > 0)) {
                readLen = FileGetLine(fh, line, lineSize, partEndOffset - count);
                count -= readLen;
                part[0].lines++;
            }
        } else {
            part[0].headerStart = 0;
            part[0].headerLen = messageSize;
            part[0].start = messageSize;
            part[0].length = 0;
            part[0].lines = 0;
        }
    }

    report = MimeReportCreate(part, maxPartNo);
    MemFree(part);
    if (allocatedSeparator) {
        MemFree(allocatedSeparator);
    }
    MemFree(header);
    return(report);
}


static int
MimeGetHelper(StoreClient *client, StoreObject *document, MimeReport **outReport)
{
    char path[XPL_MAX_PATH + 1];
    FILE *fh = NULL;
    int result = 1;
    char buffer[CONN_BUFSIZE];

    FindPathToDocument(client, document->collection_guid, document->guid, path, sizeof(path));

    fh = fopen(path, "rb");
    if (!fh) {
        result = -4;
        goto finish;
    }

    *outReport = MimeParse(fh, document->size, buffer, sizeof(buffer));
    if (!*outReport) {
        result = -5;
        goto finish;
    }

	// FIXME: cache mime information into DB?
    // (void) DStoreSetMimeReport(client->handle, info->guid, *outReport);
        
finish:
    if (fh) {
        (void) fclose(fh);
    }

    return result;
}


/* returns: 0 no report available
            1 report found
            -1 db err
            -2 db locked
            -3 no document
            -4 io err
            -5 internal err
*/


int
MimeGetInfo(StoreClient *client, StoreObject *document, MimeReport **outReport)
{
/*
  	FIXME
    DCode dcode;

    dcode = DStoreGetMimeReport(client->handle, info->guid, outReport);
    if (0 != dcode) {
        return dcode;
    }
    */

    return MimeGetHelper(client, document, outReport);
}
