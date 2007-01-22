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

#include <logger.h>

#include <mdb.h>
#include <msgapi.h>

/* Management Client Header Include */
#include <management.h>
#include "imapd.h"


BongoKeywordIndex *FetchFlagsAttIndex = NULL;
BongoKeywordIndex *FetchFlagsSoloIndex = NULL;
BongoKeywordIndex *FetchFlagsSectionIndex = NULL;

unsigned char TopLevelMime[] = "Content-Type: message/rfc822\r\nContent-Tranfer-Encoding: 7bit\r\n";
unsigned long TopLevelMimeLen = sizeof(TopLevelMime) - 1;

static BOOL
FindRFC822Header(unsigned char *Header, unsigned char *SearchFor, unsigned char **Buffer, int BufSize)
{
    unsigned char *NL, *ptr, *ptr2, *start=Header, *ptr3;
    BOOL    KeepGoing=TRUE;
    int    len;

    ptr=Header;
    while (KeepGoing) {
        NL=strchr(ptr,0x0a);
        if (NL) {
            /* Check for line continuation, if yes, whack CR/LF and keep going */
            if (IsWhiteSpace(*(NL+1))) {
                *NL=' ';
                NL[1]=' ';
                if (*(NL-1)==0x0d) {
                    *(NL-1)=' ';
                }
                continue;
            } else {
                *NL='\0';
            }
        }
        ptr2=strchr(ptr, ':');
        if (ptr2) {
            *ptr2='\0';
    
            if (XplStrCaseCmp(ptr, SearchFor)==0) {
                ptr3=ptr2+1;
                while (IsWhiteSpace(*ptr3))
                    ptr3++;
                len=strlen(ptr3);
                if (!BufSize) {
                    *Buffer=MemMalloc(len+1);
                    if (!*Buffer)
                        return(FALSE);
                } else {
                    if (len>BufSize) {
                        return(FALSE);
                    }
                }
                strncpy(*Buffer, ptr3, len);
                (*Buffer)[len]='\0';
                if ((ptr3=strrchr(*Buffer,0x0d))!=NULL)
                    *ptr3='\0';
                if ((ptr3=strrchr(*Buffer,0x0a))!=NULL)
                    *ptr3='\0';
                *ptr2=':';
                if (NL)
                    *NL=0x0a;
//    while ((ptr=strchr(*Buffer, '"'))!=0)
//     memmove(ptr, ptr+1, strlen(ptr+1)+1);
                return(TRUE);
            }
            *ptr2=':';
        }
        if (NL) {
            *NL=0x0a;
            ptr=NL+1;
        } else {
            KeepGoing=FALSE;
        }

        start=ptr;
    }
    return(FALSE);
}

static long
SendRFC822Address(ImapSession *session, unsigned char *Address, unsigned char *Answer)
{
    long ccode;
    RFC822AddressStruct *Current;
    RFC822AddressStruct *Rfc822AddressList = NULL;
    unsigned long   Len;

    RFC822ParseAddressList(&Rfc822AddressList, Address, ".MissingHostName.");

    if (Rfc822AddressList) {
        if (ConnWrite(session->client.conn, "(", 1) != -1) {
            Current = Rfc822AddressList;    

            while (Current) {
                if (Current->Personal) {
                    Len = strlen(Current->Personal);
                    if ((ccode = ConnWriteF(session->client.conn, "({%lu}\r\n", Len) != -1) && 
                        (ccode = ConnWrite(session->client.conn, Current->Personal, Len) != -1) && 
                        (ccode = ConnWrite(session->client.conn, " ", 1)) != -1) {
                        ;
                    } else {
                        break;
                    }
                } else {
                    if ((ccode = ConnWrite(session->client.conn, "(NIL ", 5)) != -1) {
                        ;
                    } else {
                        break;
                    }
                }

                if (Current->ADL) {
                    if (((ccode = ConnWrite(session->client.conn, "\"", 1)) != -1) && 
                        ((ccode = ConnWrite(session->client.conn, Current->ADL, strlen(Current->ADL))) != -1) && 
                        ((ccode = ConnWrite(session->client.conn, "\" ", 2)) != -1)) {
                        ;
                    } else {
                        break;
                    }
                } else {
                    if ((ccode = ConnWrite(session->client.conn, "NIL ", 4)) != -1) {
                        ;
                    } else {
                        break;
                    }
                }

                if (Current->Mailbox) {
                    if (((ccode = ConnWrite(session->client.conn, "\"", 1)) != -1) && 
                        ((ccode = ConnWrite(session->client.conn, Current->Mailbox, strlen(Current->Mailbox))) != -1) && 
                        ((ccode = ConnWrite(session->client.conn, "\" ", 2)) != -1)) {
                        ;
                    } else {
                        break;
                    }
                } else {
                    if ((ccode = ConnWrite(session->client.conn, "NIL ", 4)) != -1) {
                        ;
                    } else {
                        break;
                    }
                }

                if (Current->Host) {
                    if (((ccode = ConnWrite(session->client.conn, "\"", 1)) != -1) && 
                        ((ccode = ConnWrite(session->client.conn, Current->Host, strlen(Current->Host))) != -1) && 
                        ((ccode = ConnWrite(session->client.conn, "\")", 2)) != -1)) {
                        ;
                    } else {
                        break;
                    }
                } else {
                    if ((ccode = ConnWrite(session->client.conn, "NIL)", 4)) != -1) {
                        ;
                    } else {
                        break;
                    }
                }
                Current = Current->Next;
            }

            RFC822FreeAddressList(Rfc822AddressList);
            if (ccode != -1) {
                if (ConnWrite(session->client.conn, ") ", 2) != -1) {
                    return(STATUS_CONTINUE);
                }
                
            }
            return(STATUS_ABORT);
        }

        RFC822FreeAddressList(Rfc822AddressList);
        return(STATUS_ABORT);
    }

    if (ConnWrite(session->client.conn, "NIL ", 4) != -1) {
        return(STATUS_CONTINUE);
    }
    return(STATUS_ABORT);
}

static void
MakeRFC822Header(unsigned char *Header, unsigned long *HSize)
{
    unsigned char *NL, *ptr, *ptr2, *start=Header;
    BOOL    StripLine, KeepGoing=TRUE;

    ptr=Header;
    Header[*HSize]='\0';
    while (KeepGoing) {
        StripLine=FALSE;
        NL=strchr(ptr,0x0a);
        if (NL)
            *NL='\0';
        if ((*ptr!='\t') && (*ptr!=' ')) {
            ptr2=strchr(ptr, ':');
            if (ptr2) {
                *ptr2='\0';
                if (strchr(ptr,' '))
                    StripLine=TRUE;
                *ptr2=':';
            } else {
                if (NL)
                    StripLine=TRUE;
            }
        }
        if (NL) {
            *NL=0x0a;
            ptr=NL+1;
        } else {
            KeepGoing=FALSE;
        }

        if (StripLine) {
            if (NL) {
                memmove(start, ptr, strlen(ptr)+1);
                start=NL-ptr+start+1;
                ptr=start;
            } else {
                *ptr='\0';
            }
        } else {
            start=ptr;
            if ((*ptr==0x0d) || (*ptr==0x0a))
                KeepGoing=FALSE;
        }
    }
    *HSize=strlen(Header);
}

__inline static unsigned char *
GetSubHeader(unsigned char *header, unsigned long headerSize, unsigned char **fields, unsigned long fieldCount, BOOL not, unsigned long *newHeaderLen)
{
    unsigned char *newHeader;
    unsigned char *start;
    unsigned char *colon;
    unsigned char *newLine;
    unsigned char *dest;
    unsigned long i;
    unsigned long lineLen;
    BOOL keepLine;
    
    newHeader = MemMalloc(headerSize);
    if (newHeader) {
        start = header;
        dest = newHeader;
        *newHeaderLen = 0;
        do {
            colon = strchr(start, ':');
            if (colon) {
                *colon = '\0';
                i = 0;
                do {
                    if (XplStrCaseCmp(start, fields[i]) != 0) {
                        i++;
                        if (i < fieldCount) {
                            continue;
                        }

                        /* this is not a match */
                        keepLine = not;
                        break;
                    }
                    
                    /* this line is a match */
                    keepLine = !not;
                    break;
                } while (TRUE);
                *colon = ':';

                newLine = start;
                do {
                    newLine = strchr(newLine, '\n');
                    if (newLine) {
                        if (IsWhiteSpace(*(newLine + 1))) {
                            newLine +=2;
                            continue;
                        }
                    }
                    break;
                } while (TRUE);

                if (newLine) {
                    newLine++;
                    if (keepLine) {
                        lineLen = newLine - start;
                        memcpy(dest + *newHeaderLen, start, lineLen);
                        *newHeaderLen += lineLen;
                    }
                    start = newLine;
                    continue;
                }
            }

            newLine = strchr(start, '\n');
            if (newLine) {
                start = newLine + 1;
                continue;
            }

            break;
        } while(TRUE);

        if (*newHeaderLen > 0) {
            return(newHeader);
        }

        *newHeader = '\0';
        return(newHeader);
    }

    return(NULL);
}

__inline static void
ParseMIMEDLine(unsigned char *MIME, unsigned char *Type, unsigned long TypeSize, unsigned char *SubType, unsigned long SubTypeSize, unsigned char *Charset, unsigned long CharsetSize, unsigned char *Encoding, unsigned long EncodingSize, unsigned char *Name, unsigned long NameSize, long *headStart, unsigned long *headSize, unsigned long *StartPos, unsigned long *Size, unsigned long *HeaderSize, unsigned long *Lines)
{
    unsigned char *p, *p2;
    /* Type */
    p2 = strchr(MIME, ' ');
    if (p2) {
        *p2 = '\0';
        if ((unsigned long)(p2 - MIME) < TypeSize) {
            strcpy(Type, MIME);
        } else {
            strncpy(Type, MIME, TypeSize);
            Type[TypeSize] = '\0';
        }
        p = strchr(++p2, ' ');
        if (p) {
            *p = '\0';
            if ((unsigned long)(p - p2) < SubTypeSize) {
                strcpy(SubType, p2);
            } else {
                strncpy(SubType, p2, SubTypeSize);
                SubType[SubTypeSize] = '\0';
            }
            p2 = strchr(++p, ' ');
            if (p2) {
                *p2 = '\0';
                if ((unsigned long)(p2 - p) < CharsetSize) {
                    strcpy(Charset, p);
                } else {
                    strncpy(Charset, p, CharsetSize);
                    Charset[CharsetSize] = '\0';
                }
                p = strchr(++p2, '"');
                if (p) {
                    *(p-1) = '\0';
                    if ((unsigned long)(p - p2) < EncodingSize) {
                        strcpy(Encoding, p2);
                    } else {
                        strncpy(Encoding, p2, EncodingSize);
                        Encoding[EncodingSize] = '\0';
                    }
                    p2 = strchr(++p, '"');
                    if (p2) {
                        *(p2++) = '\0';

                        if ((unsigned long)(p2 - p) < NameSize) {
                            strcpy(Name, p);
                        } else {
                            strncpy(Name, p, NameSize);
                            Name[NameSize] = '\0';
                        }
                        p = strchr(++p2, ' ');
                        if (p) {
                            *headStart = atol(p2);
                            p++;
                            p2 = strchr(p, ' ');
                            if (p2) {
                                p2++;
                                *headSize = atol(p);
                                p = strchr(p2, ' ');
                                if (p) {
                                    *StartPos = atol(p2);
                                    p2 = strchr(++p, ' ');
                                    if (p2) {
                                        *Size = atol(p);
                                        p = strchr(++p2, ' ');
                                        if (p) {
                                            *HeaderSize = atol(p2);
                                            *Lines = atol(p+1);
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    return;
}

__inline static void
MessageDetailsFree(MessageDetail *messageDetail)
{
    if (messageDetail->header) {
        MemFree(messageDetail->header);
        messageDetail->header = NULL;
    }

    if (messageDetail->mimeInfo) {
        MDBDestroyValueStruct(messageDetail->mimeInfo);
        messageDetail->mimeInfo = NULL;
    }
}

__inline static void
FreeFetchFlags(FetchStruct *FetchRequest)
{
    unsigned long i;
    FetchFlagStruct *flag;

    for (i = 0; i < FetchRequest->flagCount; i++) {
        flag = &(FetchRequest->flag[i]);
        if (!(flag->hasAllocated)) {
            continue;
        }

        if (flag->hasAllocated & ALLOCATED_FIELDS) {
            MemFree(flag->fieldList.fieldRequest);
        }

        if (!(flag->hasAllocated & ALLOCATED_FIELD_POINTERS)) {
            ;
        } else {
            MemFree(flag->fieldList.field);
        }

        if (!(flag->hasAllocated & ALLOCATED_PART_NUMBERS)) {
            ;
        } else {
            MemFree(flag->bodyPart.part);
        }
    }
    return;
}

__inline static void
FreeFetchResourcesSuccess(FetchStruct *FetchRequest)
{
    if (FetchRequest->messageSet) {
        MemFree(FetchRequest->messageSet);
    }

    if (!(FetchRequest->hasAllocated)) {
        return;
    }

    FreeFetchFlags(FetchRequest);

    if (!(FetchRequest->hasAllocated & ALLOCATED_FLAGS)) {
        return;
    }
    
    MemFree(FetchRequest->flag);
    return;
}

__inline static void
FreeFetchResourcesFailure(FetchStruct *FetchRequest)
{
    MessageDetailsFree(&(FetchRequest->messageDetail));
    FreeFetchResourcesSuccess(FetchRequest);
    return;
}

__inline static long
ReadBodyPart(ImapSession *session, unsigned char **Part, unsigned long ID, unsigned long Start, unsigned long Size)
{
    long count;
    long ccode;

    *Part = MemMalloc(Size + 1);
    if (*Part) {
        if (Size) {
            /* Request the body part*/
            if (NMAPSendCommandF(session->store.conn, "READ %llx %lu %lu\r\n", session->folder.selected.message[ID].guid, Start, Size) != -1) {
                if ((ccode = NMAPReadPropertyValueLength(session->store.conn, "nmap.document", &count)) == 2001) {
                    if (NMAPReadCount(session->store.conn, *Part, count) == count) {
                        if (NMAPReadCrLf(session->store.conn) == 2) {
                            return(STATUS_CONTINUE);
                        }
                    }
                    
                    MemFree(*Part);
                    *Part = NULL;
                    return(STATUS_ABORT);
                }
                
                MemFree(*Part);
                *Part = NULL;
                return(CheckForNMAPCommError(ccode));
            }

            MemFree(*Part);
            *Part = NULL;
            return(STATUS_NMAP_COMM_ERROR);
        }

        **Part = '\0';
        return(STATUS_CONTINUE);
    }

    return(STATUS_MEMORY_ERROR);
}

__inline static long
SendEnvelope(ImapSession *session, unsigned char *Header, unsigned char *Answer)
{
    unsigned char *component = NULL;
    long ccode;
    unsigned long len;

    /* this function is memory intensive */

    if (ConnWrite(session->client.conn, "(", 1) != -1) {
        /* First comes the date, just as text */
        if (FindRFC822Header(Header, "Date", &component, 0)) {
            len = strlen(component);
            if ((ConnWriteF(session->client.conn, "{%d}\r\n", (int)len) != -1) &&
                (ConnWrite(session->client.conn, component, len) != -1) &&
                (ConnWrite(session->client.conn, " ", 1) != -1)) {
                MemFree(component);
                component = NULL;
            } else {
                MemFree(component);
                return(STATUS_ABORT);
            }
        } else {
            if (ConnWrite(session->client.conn, "NIL ", 4) != -1) {
                ;
            } else {
                return(STATUS_ABORT);
            }
        }

        /* now env_subject */
        if (FindRFC822Header(Header, "Subject", &component, 0)) {
            len=strlen(component);
            if ((ConnWriteF(session->client.conn, "{%d}\r\n", (int)len) != -1) && 
                (ConnWrite(session->client.conn, component, len) != -1) && 
                (ConnWrite(session->client.conn, " ", 1) != -1)) {
                MemFree(component);
                component = NULL;
            } else {
                MemFree(component);
                return(STATUS_ABORT);
            }
            
        } else {
            if (ConnWrite(session->client.conn, "NIL ", 4) != -1) {
                ;
            } else {
                return(STATUS_ABORT);
            }
        }

        /* now env_from */
        if (FindRFC822Header(Header, "From", &component, 0)) {
            if ((ccode = SendRFC822Address(session, component, Answer)) == STATUS_CONTINUE) {
                MemFree(component);
                component = NULL;
            } else {
                MemFree(component);
                return(ccode);
            }
        } else {
            if (ConnWrite(session->client.conn, "NIL ", 4) != -1) {
                ;
            } else {
                return(STATUS_ABORT);
            }
        }

        /* now env_sender */
        if (FindRFC822Header(Header, "Sender", &component, 0)) {
            if ((ccode = SendRFC822Address(session, component, Answer)) == STATUS_CONTINUE) {
                MemFree(component);
                component = NULL;
            } else {
                MemFree(component);
                return(ccode);
            }
        } else {
            if (FindRFC822Header(Header, "From", &component, 0)) {
                if ((ccode = SendRFC822Address(session, component, Answer)) == STATUS_CONTINUE) {
                    MemFree(component);
                    component = NULL;
                } else {
                    MemFree(component);
                    return(ccode);
                }
            } else {
                if (ConnWrite(session->client.conn, "NIL ", 4) != -1) {
                    ;
                } else {
                    return(STATUS_ABORT);
                }
            }
        }

        /* now env_in_reply_to */
        if (FindRFC822Header(Header, "Reply-To", &component, 0)) {
            if ((ccode = SendRFC822Address(session, component, Answer)) == STATUS_CONTINUE) {
                MemFree(component);
                component = NULL;
            } else {
                MemFree(component);
                return(ccode);
            }
        } else {
            if (FindRFC822Header(Header, "From", &component, 0)) {
                if ((ccode = SendRFC822Address(session, component, Answer)) == STATUS_CONTINUE) {
                    MemFree(component);
                    component = NULL;
                } else {
                    MemFree(component);
                    return(ccode);
                }
            } else {
                if (ConnWrite(session->client.conn, "NIL ", 4) != -1) {
                    ;
                } else {
                    return(STATUS_ABORT);
                }
            }
        }

        /* now env_to */
        if (FindRFC822Header(Header, "To", &component, 0)) {
            if ((ccode = SendRFC822Address(session, component, Answer)) == STATUS_CONTINUE) {
                MemFree(component);
                component = NULL;
            } else {
                MemFree(component);
                return(ccode);
            }
        } else {
            if (ConnWrite(session->client.conn, "NIL ", 4) != -1) {
                ;
            } else {
                return(STATUS_ABORT);
            }
        }

        /* now env_cc */
        if (FindRFC822Header(Header, "Cc", &component, 0)) {
            if ((ccode = SendRFC822Address(session, component, Answer)) == STATUS_CONTINUE) {
                MemFree(component);
                component = NULL;
            } else {
                MemFree(component);
                return(ccode);
            }
        } else {
            if (ConnWrite(session->client.conn, "NIL ", 4) != -1) {
                ;
            } else {
                return(STATUS_ABORT);
            }
        }

        /* now env_bcc */
        if (FindRFC822Header(Header, "Bcc", &component, 0)) {
            if ((ccode = SendRFC822Address(session, component, Answer)) == STATUS_CONTINUE) {
                MemFree(component);
                component = NULL;
            } else {
                MemFree(component);
                return(ccode);
            }
        } else {
            if (ConnWrite(session->client.conn, "NIL ", 4) != -1) {
                ;
            } else {
                return(STATUS_ABORT);
            }
        }

        /* now env_in_reply_to */
        if (FindRFC822Header(Header, "In-Reply-To", &component, 0)) {
            unsigned char *ptr = component;
            int inRepToLen = strlen(component);

            /* Break up reply to session,  Answer may not be large enough */
            /* to hold all of the recipients in the reply to field. */

            do {
                if (*ptr == '"' || *ptr == '\\' || *ptr < 0x20 || *ptr > 0x7f) {
                    if ((ConnWriteF(session->client.conn, "{%lu}\r\n", (long unsigned int)inRepToLen) != -1) && 
                        (ConnWrite(session->client.conn, component, inRepToLen) != -1) && 
                        (ConnWrite(session->client.conn, " ", 1) != -1)) {
                        break;
                    } else {
                        MemFree(component);
                        return(STATUS_ABORT);
                    }
                }
            
                if (*(++ptr)) {
                    continue;
                }

                if ((ConnWrite(session->client.conn, "\"", 1) != -1) && 
                    (ConnWrite(session->client.conn, component, inRepToLen) != -1) && 
                    (ConnWrite(session->client.conn, "\" ", 2) != -1)) {
                    break;
                } else {
                    MemFree(component);
                    return(STATUS_ABORT);
                }
            } while (TRUE);
            MemFree(component);
            component = NULL;
        } else {
            if (ConnWrite(session->client.conn, "NIL ", 4) != -1) {
                ;
            } else {
                return(STATUS_ABORT);
            }
        }

        /* now env_message_id */
        if (FindRFC822Header(Header, "Message-Id", &component, 0)) {
            if ((ConnWrite(session->client.conn, "\"", 1) != -1) && 
                (ConnWrite(session->client.conn, component, strlen(component)) != -1) && 
                (ConnWrite(session->client.conn, "\"", 1) != -1)) {
                MemFree(component);
                component = NULL;
            } else {
                MemFree(component);
                return(STATUS_ABORT);
            }
        } else {
            if (ConnWrite(session->client.conn, "NIL ", 4) != -1) {
                ;
            } else {
                return(STATUS_ABORT);
            }
        }

        if (ConnWrite(session->client.conn, ")", 1) != -1) {
            return(STATUS_CONTINUE);
        }
    }

    return(STATUS_ABORT);
}

static long 
FetchFlagResponderUid(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;

    if (ConnWriteF(session->client.conn, "UID %lu", FetchRequest->message->uid) != -1) {
        return(STATUS_CONTINUE);
    }
    return(STATUS_ABORT);
}

static long 
FetchFlagResponderFlags(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    unsigned char addSpace[2];
    long ccode;

    ccode = ConnWrite(session->client.conn, "FLAGS (", strlen("FLAGS ("));
    addSpace[0] = '\0';
    addSpace[1] = '\0';

    if ((FetchRequest->message->flags & STORE_MSG_FLAG_ANSWERED) && (ccode != -1)) {
        ccode = ConnWriteF(session->client.conn, "\\Answered");
        addSpace[0]=' ';
    } 
    if ((FetchRequest->message->flags & STORE_MSG_FLAG_DELETED) && (ccode != -1)) {
        ccode = ConnWriteF(session->client.conn, "%s\\Deleted",addSpace);
        addSpace[0]=' ';
    } 
    if ((FetchRequest->message->flags & STORE_MSG_FLAG_RECENT) && (ccode != -1)) {
        ccode = ConnWriteF(session->client.conn, "%s\\Recent", addSpace);
        addSpace[0]=' ';
    } 
    if ((FetchRequest->message->flags & STORE_MSG_FLAG_SEEN) && (ccode != -1)) {
        ccode = ConnWriteF(session->client.conn, "%s\\Seen", addSpace);
        addSpace[0]=' ';
    }
    if ((FetchRequest->message->flags & STORE_MSG_FLAG_DRAFT) && (ccode != -1)) {
        ccode = ConnWriteF(session->client.conn, "%s\\Draft", addSpace);
        addSpace[0]=' ';
    }
    if ((FetchRequest->message->flags & STORE_MSG_FLAG_FLAGGED) && (ccode != -1)) {
        ccode = ConnWriteF(session->client.conn, "%s\\Flagged", addSpace);
        addSpace[0]=' ';
    }
    
    if (ccode != -1) {
        ccode = ConnWrite(session->client.conn, ")", strlen(")"));
        if (ccode != -1) {
            return(STATUS_CONTINUE);
        }
    }
    
    return(STATUS_ABORT);
}

static long 
FetchFlagResponderEnvelope(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    if (ConnWrite(session->client.conn, "ENVELOPE ", strlen("ENVELOPE ")) != -1) {
        return(SendEnvelope(session, FetchRequest->messageDetail.header, session->store.response));
    }
    return(STATUS_ABORT);
}

static long 
FetchFlagResponderXSender(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    long ccode;
    return STATUS_CONTINUE;
    /* FIXME - the store agent does not provide this property yet */
    ccode = NMAPGetTextProperty(session->store.conn, FetchRequest->message->guid, "nmap.mail.authenticatedsender", session->store.response, sizeof(session->store.response));
    if (ccode == 3245) {
        if (ConnWrite(session->client.conn, "XSENDER {0}\r\n", 13) != -1) {
            return(STATUS_CONTINUE);
        }
        return(STATUS_ABORT);
    }

    if (ccode == 2001) {
        unsigned long authLen;

        authLen = strlen(session->store.response);
        if (!strchr(session->store.response, '@')) {
            if ((ConnWriteF(session->client.conn, "XSENDER {%lu}\r\n", authLen + 1 + Imap.server.hostnameLen) != -1) && 
                (ConnWrite(session->client.conn, session->store.response, authLen) != -1) && 
                (ConnWrite(session->client.conn, "@", 1) != -1) && 
                (ConnWrite(session->client.conn, Imap.server.hostname, Imap.server.hostnameLen) != -1)) {
                return(STATUS_CONTINUE);
            } 
            return(STATUS_ABORT);
        }

        if ((ConnWriteF(session->client.conn, "XSENDER {%lu}\r\n", authLen) != -1) && 
            (ConnWrite(session->client.conn, session->store.response, authLen) != -1)) {
            return(STATUS_CONTINUE);
        }
        return(STATUS_ABORT);
    }

    return(CheckForNMAPCommError(ccode));
}

static long
FetchFlagResponderInternalDate(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    struct tm  ltm;

    gmtime_r(&FetchRequest->message->internalDate, &ltm);

    if (ConnWriteF(session->client.conn, "INTERNALDATE \"%02d-%3s-%04d %02d:%02d:%02d +0000\"", ltm.tm_mday, Imap.command.months[ltm.tm_mon], 1900+ltm.tm_year, ltm.tm_hour, ltm.tm_min, ltm.tm_sec) != -1) {
        return(STATUS_CONTINUE);
    }
    return(STATUS_ABORT);
}

static long 
FetchFlagResponderBodyStructure(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    unsigned char **multiPartTypeList;
    long *rfcSizeList;
    long ccode;
    unsigned char Type[MIME_TYPE_LEN+1]; 
    unsigned char Subtype[MIME_SUBTYPE_LEN+1]; 
    unsigned char Charset[MIME_CHARSET_LEN+1]; 
    unsigned char Encoding[MIME_ENCODING_LEN+1]; 
    unsigned char Name[MIME_NAME_LEN+1];
    unsigned long mimeOffset;
    unsigned long mimeLen;
    unsigned long SPos; 
    unsigned long Lines; 
    unsigned long Size;
    unsigned long MPCount;
    unsigned long RFCCount;
    unsigned long partHeaderSize;
    unsigned long MultiPartTypeListLen = 20;
    unsigned long RFCSizeListLen   = 20;
    unsigned long mimeResponseLine = 0;

    multiPartTypeList = MemMalloc(sizeof(unsigned char *)*MultiPartTypeListLen);
    if (multiPartTypeList) {
        MPCount = 0;

        rfcSizeList = MemMalloc(sizeof(long)*RFCSizeListLen);
        if (rfcSizeList) {
            RFCCount=0;

            if (FetchRequest->flags & F_BODY) {
                ccode = ConnWrite(session->client.conn, "BODY ", strlen("BODY "));
            } else {
                ccode = ConnWrite(session->client.conn, "BODYSTRUCTURE ", strlen("BODYSTRUCTURE "));
            }

            if (ccode != -1) {
                do {
                    switch(atol(FetchRequest->messageDetail.mimeInfo->Value[mimeResponseLine])) {
                        case 2002: {
                            ParseMIMEDLine(FetchRequest->messageDetail.mimeInfo->Value[mimeResponseLine] + 5, 
                                          Type, MIME_TYPE_LEN, 
                                          Subtype, MIME_SUBTYPE_LEN, 
                                          Charset, MIME_CHARSET_LEN, 
                                          Encoding, MIME_ENCODING_LEN, 
                                          Name, MIME_NAME_LEN, 
                                          &mimeOffset,
                                          &mimeLen,
                                          &SPos, 
                                          &Size, 
                                          &partHeaderSize, 
                                          &Lines);  

                            /* First, set the default values if nothing is specified by the message */
                            if (Type[0]=='-') {
                                strcpy(Type, "TEXT");
                                strcpy(Subtype, "PLAIN");
                            }

                            if (Encoding[0]=='-') {
                                strcpy(Encoding, "7BIT");
                            }

                            /************
                             **MULTIPART**
                             ************/
                            if (XplStrCaseCmp(Type, "Multipart")==0) {
                                if (MPCount < MultiPartTypeListLen) {  
                                    multiPartTypeList[MPCount++] = MemStrdup(Subtype);
                                } else {
                                    unsigned char *tmp;

                                    MultiPartTypeListLen <<= 1;  /* Double the Size */
                                    tmp = MemRealloc(multiPartTypeList, sizeof(unsigned char *) * MultiPartTypeListLen);
                                    if (tmp) {
                                        multiPartTypeList = (unsigned char **)tmp; 
                                        multiPartTypeList[MPCount++] = MemStrdup(Subtype);
                                    } else {
                                        while (MPCount > 0) {
                                            MPCount--;           
                                            MemFree(multiPartTypeList[MPCount]);
                                        }
                                        MemFree(multiPartTypeList);
                                        MemFree(rfcSizeList);
                                        return(STATUS_MEMORY_ERROR);
                                    } 
                                }
                                if (ConnWrite(session->client.conn, "(", 1) != -1) {
                                    ;
                                } else {
                                    MemFree(multiPartTypeList);
                                    MemFree(rfcSizeList);
                                    return(STATUS_ABORT);
                                }
                                /************
                                 **   TEXT  **
                                 ************/
                            } else if (XplStrCaseCmp(Type, "Text")==0) {
                                if (Charset[0]=='-') {
                                    strcpy(Charset, "US-ASCII");
                                }
                                if (ConnWriteF(session->client.conn, "(\"%s\" \"%s\" (\"CHARSET\" \"%s\") NIL NIL \"%s\" %lu %lu)", Type, Subtype, Charset, Encoding, Size, Lines) != -1) {
                                    ;
                                } else {
                                    MemFree(multiPartTypeList);
                                    MemFree(rfcSizeList);
                                    return(STATUS_ABORT);
                                }
                                /************
                                 ** MESSAGE **
                                 ************/
                            } else if ((XplStrCaseCmp(Type, "Message")==0) && (XplStrCaseCmp(Subtype, "RFC822")==0)) {
                                unsigned char *PartHeader;

                                /* we have to print the basic stuff, then get the headers, print the envelope */
                                /* and then continue in the loop */
                                if (RFCCount < RFCSizeListLen) {
                                    rfcSizeList[RFCCount++] = Lines;
                                } else {
                                    unsigned char *tmp;

                                    RFCSizeListLen <<= 1;  /* Double the Size */
                                    tmp = MemRealloc(rfcSizeList, sizeof(long) * RFCSizeListLen);
                                    if (tmp) {
                                        rfcSizeList = (long *)tmp; 
                                        rfcSizeList[RFCCount++] = Lines;
                                    } else {
                                        while (MPCount > 0) {
                                            MPCount--;           
                                            MemFree(multiPartTypeList[MPCount]);
                                        }
                                        MemFree(multiPartTypeList);
                                        MemFree(rfcSizeList);
                                        return(STATUS_MEMORY_ERROR);
                                    } 
                                }

                                /* Body Type & Subtype */
                                if (ConnWriteF(session->client.conn, "(\"%s\" \"%s\" ", Type, Subtype) != -1) {
                                    ;
                                } else {
                                    MemFree(multiPartTypeList);
                                    MemFree(rfcSizeList);
                                    return(STATUS_ABORT);
                                }

                                /* Body parameter parentesized list */
                                if ((Charset[0]!='-') && (Name[0]!='\0')) {
                                    unsigned char *langPtr;
                                    if ((Name[0] != '*') || (Name[1] != '=') || ((langPtr = strchr(Name + 2, '\'')) == NULL) || ((langPtr = strchr(langPtr + 1, '\'')) == NULL)) {
                                        if (ConnWriteF(session->client.conn, "(\"CHARSET\" \"%s\" \"NAME\" \"%s\") ", Charset, Name) != -1) {
                                            ;
                                        } else {
                                            MemFree(multiPartTypeList);
                                            MemFree(rfcSizeList);
                                            return(STATUS_ABORT);
                                        }
                                    } else {
                                        /* this is an rfc2231 name */
                                        
                                        if (ConnWriteF(session->client.conn, "(\"CHARSET\" \"%s\" \"NAME*\" \"%s\") ", Charset, Name + 2) != -1) {
                                            ;
                                        } else {
                                            MemFree(multiPartTypeList);
                                            MemFree(rfcSizeList);
                                            return(STATUS_ABORT);
                                        }
                                    }
                                } else if (Charset[0]!='-') {
                                    if (ConnWriteF(session->client.conn, "(\"CHARSET\" \"%s\") ", Charset) != -1) {
                                        ;
                                    } else {
                                        MemFree(multiPartTypeList);
                                        MemFree(rfcSizeList);
                                        return(STATUS_ABORT);
                                    }

                                } else if (Name[0]!='\0') {
                                    unsigned char *langPtr;
                                    if ((Name[0] != '*') || (Name[1] != '=') || ((langPtr = strchr(Name + 2, '\'')) == NULL) || ((langPtr = strchr(langPtr + 1, '\'')) == NULL)) {
                                        if (ConnWriteF(session->client.conn, "(\"NAME\" \"%s\") ", Name) != -1) {
                                            ;
                                        } else {
                                            MemFree(multiPartTypeList);
                                            MemFree(rfcSizeList);
                                            return(STATUS_ABORT);
                                        }

                                    } else {
                                        /* this is an rfc2231 name */
                                        if (ConnWriteF(session->client.conn, "(\"NAME*\" \"%s\") ", Name + 2) != -1) {
                                            ;
                                        } else {
                                            MemFree(multiPartTypeList);
                                            MemFree(rfcSizeList);
                                            return(STATUS_ABORT);
                                        }
                                    }
                                } else {
                                    if (ConnWrite(session->client.conn, "NIL ", 4) != -1) {
                                        ;
                                    } else {
                                        MemFree(multiPartTypeList);
                                        MemFree(rfcSizeList);
                                        return(STATUS_ABORT);
                                    }
                                }

                                /* Body ID & Body description */
                                if (ConnWrite(session->client.conn, "NIL NIL ", 8) != -1) {
                                    ;
                                } else {
                                    MemFree(multiPartTypeList);
                                    MemFree(rfcSizeList);
                                    return(STATUS_ABORT);
                                }

                                /* Body encoding && Size */
                                if (ConnWriteF(session->client.conn, "\"%s\" %lu ", Encoding, Size) != -1) {
                                    ;
                                } else {
                                    MemFree(multiPartTypeList);
                                    MemFree(rfcSizeList);
                                    return(STATUS_ABORT);
                                }

                                /* Grab the envelope  */
                            
                                if ((ccode = ReadBodyPart(session, &PartHeader, FetchRequest->messageDetail.sequenceNumber, SPos, partHeaderSize)) == STATUS_CONTINUE) {
                                    ccode = SendEnvelope(session, PartHeader, session->store.response);
                                    MemFree(PartHeader);
                                    if (ccode == STATUS_CONTINUE) {
                                        ;
                                    } else {
                                        MemFree(multiPartTypeList);
                                        MemFree(rfcSizeList);
                                        return(ccode);
                                    }
                                } else {
                                    MemFree(multiPartTypeList);
                                    MemFree(rfcSizeList);
                                    return(ccode);
                                }

                                /************
                                 ** DEFAULT **
                                 ************/
                            } else {
                                /* Body Type & Subtype */
                                if (ConnWriteF(session->client.conn, "(\"%s\" \"%s\" ", Type, Subtype) != -1) {
                                    ;
                                } else {
                                    MemFree(multiPartTypeList);
                                    MemFree(rfcSizeList);
                                    return(STATUS_ABORT);
                                }

                                /* Body parameter parentesized list */
                                if ((Charset[0]!='-') && (Name[0]!='\0')) {
                                    unsigned char *langPtr;
                                    if ((Name[0] != '*') || (Name[1] != '=') || ((langPtr = strchr(Name + 2, '\'')) == NULL) || ((langPtr = strchr(langPtr + 1, '\'')) == NULL)) {
                                        if (ConnWriteF(session->client.conn, "(\"CHARSET\" \"%s\" \"NAME\" \"%s\") ", Charset, Name) != -1) {
                                            ;
                                        } else {
                                            MemFree(multiPartTypeList);
                                            MemFree(rfcSizeList);
                                            return(STATUS_ABORT);
                                        }
                                    } else {
                                        /* This is an rfc2231 encoded name */
                                        if (ConnWriteF(session->client.conn, "(\"CHARSET\" \"%s\" \"NAME*\" \"%s\") ", Charset, Name + 2) != -1) {
                                            ;
                                        } else {
                                            MemFree(multiPartTypeList);
                                            MemFree(rfcSizeList);
                                            return(STATUS_ABORT);
                                        }
                                    }
                                } else if (Charset[0]!='-') {
                                    if (ConnWriteF(session->client.conn, "(\"CHARSET\" \"%s\") ", Charset) != -1) {
                                        ;
                                    } else {
                                        MemFree(multiPartTypeList);
                                        MemFree(rfcSizeList);
                                        return(STATUS_ABORT);
                                    }
                                } else if (Name[0]!='\0') {
                                    unsigned char *langPtr;
                                    if ((Name[0] != '*') || (Name[1] != '=') || ((langPtr = strchr(Name + 2, '\'')) == NULL) || ((langPtr = strchr(langPtr + 1, '\'')) == NULL)) {
                                        if (ConnWriteF(session->client.conn, "(\"NAME\" \"%s\") ", Name) != -1) {
                                            ;
                                        } else {
                                            MemFree(multiPartTypeList);
                                            MemFree(rfcSizeList);
                                            return(STATUS_ABORT);
                                        }

                                    } else {
                                        /* This is an rfc2231 encoded name */
                                        if (ConnWriteF(session->client.conn, "(\"NAME*\" \"%s\") ", Name + 2) != -1) {
                                            ;
                                        } else {
                                            MemFree(multiPartTypeList);
                                            MemFree(rfcSizeList);
                                            return(STATUS_ABORT);
                                        }
                                    }
                                } else {
                                    if (ConnWrite(session->client.conn, "NIL ", 4) != -1) {
                                        ;
                                    } else {
                                        MemFree(multiPartTypeList);
                                        MemFree(rfcSizeList);
                                        return(STATUS_ABORT);
                                    }
                                }

                                /* Body ID & Body description */
                                if (ConnWrite(session->client.conn, "NIL NIL ", 8) != -1) {
                                    ;
                                } else {
                                    MemFree(multiPartTypeList);
                                    MemFree(rfcSizeList);
                                    return(STATUS_ABORT);
                                }

                                /* Body encoding && Size */
                                if (ConnWriteF(session->client.conn, "\"%s\" %lu)", Encoding, Size) != -1) {
                                    ;
                                } else {
                                    MemFree(multiPartTypeList);
                                    MemFree(rfcSizeList);
                                    return(STATUS_ABORT);
                                }
                            }
                            break;
                        }

                        case 2003: {
                            if (MPCount > 0) {
                                MPCount--;
                                if (ConnWriteF(session->client.conn, " \"%s\")", multiPartTypeList[MPCount]) != -1) {
                                    ;
                                } else {
                                    MemFree(multiPartTypeList);
                                    MemFree(rfcSizeList);
                                    return(STATUS_ABORT);
                                }

                                MemFree(multiPartTypeList[MPCount]);
                                multiPartTypeList[MPCount] = NULL;
                            }
                            break;
                        }

                        case 2004: {
                            RFCCount--;
                            if (ConnWriteF(session->client.conn, " %lu)", rfcSizeList[RFCCount]) != -1) {
                                ;
                            } else {
                                MemFree(multiPartTypeList);
                                MemFree(rfcSizeList);
                                return(STATUS_ABORT);
                            }

                            break;
                        }
                    }

                    mimeResponseLine++;
                    if (mimeResponseLine < FetchRequest->messageDetail.mimeInfo->Used) {
                        continue;
                    }

                    break;
                } while (TRUE);

                MemFree(multiPartTypeList);
                MemFree(rfcSizeList);
                return(STATUS_CONTINUE);
            }
            MemFree(multiPartTypeList);
            MemFree(rfcSizeList);
            return(STATUS_ABORT);
        }
        MemFree(multiPartTypeList);
    }

    return(STATUS_MEMORY_ERROR);
}

static long 
FetchFlagResponderRfc822Text(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    long ccode;
    long count;

    if (NMAPSendCommandF(session->store.conn, "READ %llx %lu\r\n", FetchRequest->message->guid, FetchRequest->message->headerSize) != -1) {
        if ((ccode = NMAPReadPropertyValueLength(session->store.conn, "nmap.document", &count)) == 2001) {
            if (ConnWriteF(session->client.conn, "RFC822.TEXT {%lu}\r\n", count) != -1) {
                if (ConnReadToConn(session->store.conn, session->client.conn, count) == count) {
                    if (NMAPReadCrLf(session->store.conn) == 2) {
                        return(STATUS_CONTINUE);
                    }
                }
            }
            return(STATUS_ABORT);
        }

        return(CheckForNMAPCommError(ccode));
    }

    return(STATUS_NMAP_COMM_ERROR);
}

static long 
FetchFlagResponderRfc822Size(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;

    if(ConnWriteF(session->client.conn, "RFC822.SIZE %lu", FetchRequest->message->size) != -1) {
        return(STATUS_CONTINUE);
    }
    return(STATUS_ABORT);
}

static long 
FetchFlagResponderRfc822Header(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;

    if (ConnWriteF(session->client.conn, "RFC822.HEADER {%lu}\r\n", FetchRequest->message->headerSize) != -1) {
        if (ConnWrite(session->client.conn, FetchRequest->messageDetail.header, FetchRequest->message->headerSize) != -1) {
            return(STATUS_CONTINUE);
        }
    }
    return(STATUS_ABORT);
}

static long
FetchFlagResponderRfc822(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    long ccode;
    long count;

    if (NMAPSendCommandF(session->store.conn, "READ %llx %lu\r\n", FetchRequest->message->guid, FetchRequest->message->headerSize) != -1) {
        if ((ccode = NMAPReadPropertyValueLength(session->store.conn, "nmap.document", &count)) == 2001) {
            if (ConnWriteF(session->client.conn, "RFC822 {%lu}\r\n", count + FetchRequest->message->headerSize) != -1) {
                if (ConnWrite(session->client.conn, FetchRequest->messageDetail.header, FetchRequest->message->headerSize) != -1) {
                    if (ConnReadToConn(session->store.conn, session->client.conn, count) == count) {
                        if (NMAPReadCrLf(session->store.conn) == 2) {
                            return(STATUS_CONTINUE);
                        }
                    }
                }
            }
           
            return(STATUS_ABORT);
        }

        return(CheckForNMAPCommError(ccode));
    }

    return(STATUS_NMAP_COMM_ERROR);
}

static long
FetchFlagResponderBody(void *param1, void *param2, void *param3)
{
    return(FetchFlagResponderBodyStructure(param1, param2, param3));
}

static long 
FetchFlagResponderBodyHeader(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;

    if (ConnWriteF(session->client.conn, "BODY[HEADER] {%lu}\r\n", FetchRequest->message->headerSize) != -1) {
        if (ConnWrite(session->client.conn, FetchRequest->messageDetail.header, FetchRequest->message->headerSize) != -1) {
            return(STATUS_CONTINUE);
        }
    }
    return(STATUS_ABORT);
}

static long
FetchFlagResponderBodyHeaderPartial(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    FetchFlagStruct *flag = (FetchFlagStruct *)param3;

    if (flag->partial.start < FetchRequest->message->headerSize) {
        if ((flag->partial.start + flag->partial.len) < FetchRequest->message->headerSize) {
            if (ConnWriteF(session->client.conn, "BODY[HEADER]<%lu> {%lu}\r\n", flag->partial.start, flag->partial.len) != -1) {
                if (ConnWrite(session->client.conn, FetchRequest->messageDetail.header + flag->partial.start, flag->partial.len) != -1) {
                    return(STATUS_CONTINUE);
                }
            }
            return(STATUS_ABORT);
        }

        if (ConnWriteF(session->client.conn, "BODY[HEADER]<%lu> {%lu}\r\n", flag->partial.start, FetchRequest->message->headerSize - flag->partial.start) != -1) {
            if (ConnWrite(session->client.conn, FetchRequest->messageDetail.header + flag->partial.start, FetchRequest->message->headerSize - flag->partial.start) != -1) {
                return(STATUS_CONTINUE);
            }
        }
        return(STATUS_ABORT);
    }

    if (ConnWriteF(session->client.conn, "BODY[HEADER]<%lu> {0}\r\n", flag->partial.start) != -1) {
        return(STATUS_CONTINUE);
    }
    return(STATUS_ABORT);
}

static long
FetchFlagResponderBodyText(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    long ccode;

    long count;

    if (NMAPSendCommandF(session->store.conn, "READ %llx %lu\r\n", FetchRequest->message->guid, FetchRequest->message->headerSize) != -1) {
        if ((ccode = NMAPReadPropertyValueLength(session->store.conn, "nmap.document", &count)) == 2001) {
            if (ConnWriteF(session->client.conn, "BODY[TEXT] {%lu}\r\n", count) != -1) {
                if (ConnReadToConn(session->store.conn, session->client.conn, count) == count) {
                    if (NMAPReadCrLf(session->store.conn) == 2) {
                        return(STATUS_CONTINUE);
                    }
                }
            }

            return(STATUS_ABORT);
        }
    
        return(CheckForNMAPCommError(ccode));
    }

    return(STATUS_NMAP_COMM_ERROR);
}

static long
FetchFlagResponderBodyTextPartial(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    FetchFlagStruct *flag = (FetchFlagStruct *)param3;
    long count;
    long ccode;

    if (flag->partial.start < FetchRequest->message->bodySize) {
        if ((flag->partial.start + flag->partial.len) < FetchRequest->message->bodySize) {
            ccode = NMAPSendCommandF(session->store.conn, "READ %llx %lu %lu\r\n", FetchRequest->message->guid, FetchRequest->message->headerSize + flag->partial.start, flag->partial.len);
        } else {
            ccode = NMAPSendCommandF(session->store.conn, "READ %llx %lu %lu\r\n", FetchRequest->message->guid, FetchRequest->message->headerSize + flag->partial.start, FetchRequest->message->bodySize - flag->partial.start);
        }

        if (ccode != -1) {
            if ((ccode = NMAPReadPropertyValueLength(session->store.conn, "nmap.document", &count)) == 2001) {
                if (ConnWriteF(session->client.conn, "BODY[TEXT]<%lu> {%lu}\r\n", flag->partial.start, count) != -1) {
                    if (ConnReadToConn(session->store.conn, session->client.conn, count) == count) {
                        if (NMAPReadCrLf(session->store.conn) == 2) {
                            return(STATUS_CONTINUE);
                        }
                    }
                }

                return(STATUS_ABORT);
            }

            return(CheckForNMAPCommError(ccode));
        }

        return(STATUS_NMAP_COMM_ERROR);
    }

    if (ConnWriteF(session->client.conn, "BODY[TEXT]<%lu> {0}\r\n", flag->partial.start) != -1) {
        return(STATUS_CONTINUE);
    }
    return(STATUS_ABORT);
}

static long
FetchFlagResponderBodyBoth(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    long ccode;
    long count;

    if (NMAPSendCommandF(session->store.conn, "READ %llx\r\n", FetchRequest->message->guid) != -1) {
        if ((ccode = NMAPReadPropertyValueLength(session->store.conn, "nmap.document", &count)) == 2001) {
            if (ConnWriteF(session->client.conn, "BODY[] {%lu}\r\n", count) != -1) {
                if (ConnReadToConn(session->store.conn, session->client.conn, count) == count) {
                    if (NMAPReadCrLf(session->store.conn) == 2) {
                        return(STATUS_CONTINUE);
                    }
                }
            }

            return(STATUS_ABORT);
        }

        return(CheckForNMAPCommError(ccode));
    }

    return(STATUS_NMAP_COMM_ERROR);
}

static long
FetchFlagResponderBodyBothPartial(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    FetchFlagStruct *flag = (FetchFlagStruct *)param3;
    long ccode;
    long count;

    if (flag->partial.start < FetchRequest->message->size) {
        if (NMAPSendCommandF(session->store.conn, "READ %llx %d %lu\r\n", FetchRequest->message->guid, flag->partial.start, flag->partial.len) != -1) {
            if ((ccode = NMAPReadPropertyValueLength(session->store.conn, "nmap.document", &count)) == 2001) {
                if (ConnWriteF(session->client.conn, "BODY[]<%lu> {%lu}\r\n", flag->partial.start, count) != -1) {
                    if (ConnReadToConn(session->store.conn, session->client.conn, count) == count) {
                        if (NMAPReadCrLf(session->store.conn) == 2) {
                            return(STATUS_CONTINUE);
                        }
                    }
                }

                return(STATUS_ABORT);
            }

            return(CheckForNMAPCommError(ccode));
        }

        return(STATUS_NMAP_COMM_ERROR);
    }

    if (ConnWriteF(session->client.conn, "BODY[]<%lu> {0}\r\n", flag->partial.start) != -1) {
        return(STATUS_CONTINUE);
    }
    return(STATUS_ABORT);
}

static long 
FetchFlagResponderBodyMime(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;

    if (ConnWriteF(session->client.conn, "BODY[MIME] {%lu}\r\n", TopLevelMimeLen) != -1) {
        if (ConnWrite(session->client.conn, TopLevelMime, TopLevelMimeLen) != -1) {
            return(STATUS_CONTINUE);
        }
    }
    return(STATUS_ABORT);
}

static long
FetchFlagResponderBodyMimePartial(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchFlagStruct *flag = (FetchFlagStruct *)param3;

    if (flag->partial.start < TopLevelMimeLen) {
        if ((flag->partial.start + flag->partial.len) < TopLevelMimeLen) {
            if (ConnWriteF(session->client.conn, "BODY[MIME]<%lu> {%lu}\r\n", flag->partial.start, flag->partial.len) != -1) {
                if (ConnWrite(session->client.conn, TopLevelMime + flag->partial.start, flag->partial.len) != -1) {
                    return(STATUS_CONTINUE);
                }
            }
            return(STATUS_ABORT);
        }

        if (ConnWriteF(session->client.conn, "BODY[MIME]<%lu> {%lu}\r\n", flag->partial.start, TopLevelMimeLen - flag->partial.start) != -1) {
            if (ConnWrite(session->client.conn, TopLevelMime + flag->partial.start, TopLevelMimeLen - flag->partial.start) != -1) {
                return(STATUS_CONTINUE);
            }
        }
        return(STATUS_ABORT);
    }

    if (ConnWriteF(session->client.conn, "BODY[MIME]<%lu> {0}\r\n", flag->partial.start) != -1) {
        return(STATUS_CONTINUE);
    }
    return(STATUS_ABORT);
}

static long
FetchFlagResponderBodyHeaderFields(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    FetchFlagStruct *flag = (FetchFlagStruct *)param3;
    long ccode;
    unsigned long j;
    unsigned char *resultHeader;
    unsigned long resultHeaderLen;

    if (!flag->fieldList.skip) {
        ccode = ConnWrite(session->client.conn, "BODY[HEADER.FIELDS (", strlen("BODY[HEADER.FIELDS ("));
    } else {
        ccode = ConnWrite(session->client.conn, "BODY[HEADER.FIELDS.NOT (", strlen("BODY[HEADER.FIELDS.NOT ("));
    }

    if (ccode != -1) {
        /* We go one less to have an easy way of knowing if to put a space */
        for (j = 0; j < flag->fieldList.count - 1; j++) {
            if (ConnWriteF(session->client.conn, "\"%s\" ", flag->fieldList.field[j]) != -1) {
                continue;
            }

            return(STATUS_ABORT);
        } 
        
        if (ConnWriteF(session->client.conn, "\"%s\")]", flag->fieldList.field[j]) != -1) {
            resultHeader = GetSubHeader(FetchRequest->messageDetail.header, FetchRequest->message->headerSize, flag->fieldList.field, flag->fieldList.count, flag->fieldList.skip, &resultHeaderLen);
            if (resultHeader) {
                if (!flag->isPartial) {
                    if (resultHeaderLen > 0) {
                        if (ConnWriteF(session->client.conn, " {%lu}\r\n", resultHeaderLen + 2) != -1) {
                            if (ConnWrite(session->client.conn, resultHeader, resultHeaderLen) != -1) {
                                if (ConnWrite(session->client.conn, "\r\n", 2) != -1) {                               
                                    MemFree(resultHeader);
                                    return(STATUS_CONTINUE);
                                }
                            }
                        }

                        MemFree(resultHeader);
                        return(STATUS_ABORT);
                    }

                    if (ConnWrite(session->client.conn, " {0}\r\n", sizeof(" {0}\r\n") - 1) != -1) {
                        MemFree(resultHeader);
                        return(STATUS_CONTINUE);
                    }

                    MemFree(resultHeader);
                    return(STATUS_ABORT);
                }

                if (resultHeaderLen > 0) {
                    if (flag->partial.start < resultHeaderLen) {
                        if ((flag->partial.start + flag->partial.len) < resultHeaderLen) {
                            ;
                        } else {
                            flag->partial.len = resultHeaderLen - flag->partial.start;
                        }

                    } else {
                        flag->partial.start = 0;
                        flag->partial.len = 0;
                    }

                    if (flag->partial.len > 0) {
                        if ((ccode = ConnWriteF(session->client.conn, "<%lu> {%lu}\r\n", flag->partial.start, flag->partial.len)) != -1) {
                            ccode = ConnWrite(session->client.conn, resultHeader + flag->partial.start, flag->partial.len);
                        }
                    } else {
                        ccode = ConnWriteF(session->client.conn, "<%lu> {0}\r\n", flag->partial.start);
                    }

                    MemFree(resultHeader);
                    if (ccode != -1) {
                        return(STATUS_CONTINUE);
                    }
                    return(STATUS_ABORT);
                }

                if (ConnWriteF(session->client.conn, "<%lu> {0}\r\n", flag->partial.start) != -1) {
                    MemFree(resultHeader);
                    return(STATUS_CONTINUE);
                }

                MemFree(resultHeader);
                return(STATUS_ABORT);
            }
            return(STATUS_MEMORY_ERROR);
        }
    }

    return(STATUS_ABORT);
}

__inline static BOOL
HasPart(MDBValueStruct *mimeInfo, BodyPartRequestStruct *request)
{
    unsigned char type[MIME_TYPE_LEN+1];
    unsigned char dummy[MIME_NAME_LEN+1];
    unsigned long dummyLong;

    long depthChange;

    BOOL haveRfc822 = TRUE;
    unsigned long mimeResponseLine = 0;
    unsigned long currentDepth = 0;

    unsigned long matchDepth = 0;
    unsigned long matchDepthPart = 0;

    do {
        switch (atol(mimeInfo->Value[mimeResponseLine])) {
            case 2002: {
                ParseMIMEDLine(mimeInfo->Value[mimeResponseLine] + 5, 
                               type, MIME_TYPE_LEN, 
                               dummy, MIME_NAME_LEN, 
                               dummy, MIME_NAME_LEN, 
                               dummy, MIME_NAME_LEN, 
                               dummy, MIME_NAME_LEN, 
                               &(request->mimeOffset), 
                               &(request->mimeLen), 
                               &(request->partOffset), 
                               &(request->partLen), 
                               &(request->messageHeaderLen), 
                               &dummyLong);  

                if (toupper(type[0]) == 'M') {
                    if (XplStrCaseCmp(type, "MULTIPART") == 0) {
                        if (!haveRfc822) {
                            if (currentDepth == matchDepth) {
                                matchDepthPart++;
                            }
                            depthChange = 1;
                        } else {
                            depthChange = 0;
                        }
                        haveRfc822 = FALSE;
                    } else if (XplStrCaseCmp(type, "MESSAGE") == 0) {
                        if (!haveRfc822) {
                            if (currentDepth == matchDepth) {
                                matchDepthPart++;
                            }
                            depthChange = 1;
                            haveRfc822 = TRUE;
                        } else {
                            depthChange = 0;
                        }
                    } else {
                        if (currentDepth == matchDepth) {
                            matchDepthPart++;
                        }
                        haveRfc822 = FALSE;
                        depthChange = 0;
                    }
                } else {
                    if (currentDepth == matchDepth) {
                        matchDepthPart++;
                    }
                    depthChange = 0;
                    haveRfc822 = FALSE;
                }

                /* Check to see if this is the part we are looking for */
                if (currentDepth == matchDepth) {
                    if (matchDepthPart == request->part[matchDepth]) {
                        matchDepth++;
                        if (matchDepth == request->depth) {
                            /* We found the part!!!! */
                            request->isMessage = haveRfc822;
                            return(TRUE);

                        }
                
                        matchDepthPart = 0;
                    } else if (matchDepthPart > request->part[matchDepth]) {
                        /* Not Found! Send Nothing */
                        return(FALSE);
                    }
                } else if (currentDepth < matchDepth) {
                    /* Not Found! Send Nothing */
                    return(FALSE);
                }

                break;
            }

            case 2003:
            case 2004: {
                if (currentDepth > 0) {
                    depthChange = -1;
                } else {
                    depthChange = 0;
                }
                break;
            }
        }

        mimeResponseLine++;
        if (mimeResponseLine < mimeInfo->Used) {
            if (depthChange == 0) {
                continue;
            }

            if (depthChange > 0) {
                currentDepth++;
                if (currentDepth == matchDepth) {
                    matchDepthPart = 0;
                }
                continue;
            }
            
            currentDepth--;
            continue;
            
        }

        /* Not Found; Send Nothing */
        break;
    } while(TRUE);

    return(FALSE);
}

static long 
FetchFlagResponderBodyPartNumber(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    FetchFlagStruct *flag = (FetchFlagStruct *)param3;
    long ccode;
    long count;
    unsigned long i;

    if (ConnWriteF(session->client.conn, "BODY[%lu", flag->bodyPart.part[0]) != -1) {
        for (i = 1; i < flag->bodyPart.depth; i++) {
            if (ConnWriteF(session->client.conn, ".%lu", flag->bodyPart.part[i]) != -1) {
                continue;
            }
            return(STATUS_ABORT);
        }

        if (HasPart(FetchRequest->messageDetail.mimeInfo, &(flag->bodyPart))) {
            if (NMAPSendCommandF(session->store.conn, "READ %llx %lu %lu\r\n", FetchRequest->message->guid, flag->bodyPart.partOffset, flag->bodyPart.partLen) != -1) {
                if ((ccode = NMAPReadPropertyValueLength(session->store.conn, "nmap.document", &count)) == 2001) {
                    if (ConnWriteF(session->client.conn, "] {%lu}\r\n", count) != -1) {
                        if (ConnReadToConn(session->store.conn, session->client.conn, count) == count) {
                            if (NMAPReadCrLf(session->store.conn) == 2) {
                                return(STATUS_CONTINUE);
                            }
                        }
                    }

                    return(STATUS_ABORT);
                }
    
                return(CheckForNMAPCommError(ccode));
            }

            return(STATUS_NMAP_COMM_ERROR);
        }

        if (ConnWrite(session->client.conn, "] {0}\r\n", strlen("] {0}\r\n")) != -1) {
            return(STATUS_CONTINUE);
        }
    }
    return(STATUS_ABORT);
}

static long 
FetchFlagResponderBodyPartNumberPartial(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    FetchFlagStruct *flag = (FetchFlagStruct *)param3;
    long ccode;
    long count;
    unsigned long i;
    long resultLen;

    if (ConnWriteF(session->client.conn, "BODY[%lu", flag->bodyPart.part[0]) != -1) {
        for (i = 1; i < flag->bodyPart.depth; i++) {
            if (ConnWriteF(session->client.conn, ".%lu", flag->bodyPart.part[i]) != -1) {
                continue;
            }
            return(STATUS_ABORT);
        }

        if (HasPart(FetchRequest->messageDetail.mimeInfo, &(flag->bodyPart))) {
            if (flag->partial.start < flag->bodyPart.partLen) {
                if ((flag->partial.start + flag->partial.len) < flag->bodyPart.partLen) {
                    /* all in range */
                    resultLen = flag->partial.len;
                } else {
                    /* len out of range */
                    resultLen = flag->bodyPart.partLen - flag->partial.start;
                }

                if (NMAPSendCommandF(session->store.conn, "READ %llx %lu %lu\r\n", FetchRequest->message->guid, flag->bodyPart.partOffset + flag->partial.start, resultLen) != -1) {
                    if ((ccode = NMAPReadPropertyValueLength(session->store.conn, "nmap.document", &count)) == 2001) {
                        if (ConnWriteF(session->client.conn, "]<%lu> {%lu}\r\n", flag->partial.start, count) != -1) {
                            if (ConnReadToConn(session->store.conn, session->client.conn, count) == count) {
                                if (NMAPReadCrLf(session->store.conn) == 2) {
                                    return(STATUS_CONTINUE);
                                }
                            }
                        }

                        return(STATUS_ABORT);
                    }

                    return(CheckForNMAPCommError(ccode));
                }

                return(STATUS_NMAP_COMM_ERROR);
            }

            /* start out of range */
        }

        if (ConnWriteF(session->client.conn, "]<%lu> {0}\r\n", flag->partial.start) != -1) {
            return(STATUS_CONTINUE);
        }
    }

    return(STATUS_ABORT);
}

static long 
FetchFlagResponderBodyPartText(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    FetchFlagStruct *flag = (FetchFlagStruct *)param3;
    long ccode;
    long count;
    unsigned long i;

    if (ConnWriteF(session->client.conn, "BODY[%lu", flag->bodyPart.part[0]) != -1) {
        for (i = 1; i < flag->bodyPart.depth; i++) {
            if (ConnWriteF(session->client.conn, ".%lu", flag->bodyPart.part[i]) != -1) {
                continue;
            }
            return(STATUS_ABORT);
        }
        
        if (HasPart(FetchRequest->messageDetail.mimeInfo, &(flag->bodyPart)) && flag->bodyPart.isMessage) {
            if (NMAPSendCommandF(session->store.conn, "READ %llx %lu %lu\r\n", FetchRequest->message->guid, flag->bodyPart.partOffset + flag->bodyPart.messageHeaderLen, flag->bodyPart.partLen - flag->bodyPart.messageHeaderLen) != -1) {
                if ((ccode = NMAPReadPropertyValueLength(session->store.conn, "nmap.document", &count)) == 2001) {
                    if (ConnWriteF(session->client.conn, ".TEXT] {%lu}\r\n", count) != -1) {
                        if (ConnReadToConn(session->store.conn, session->client.conn, count) == count) {
                            if (NMAPReadCrLf(session->store.conn) == 2) {
                                return(STATUS_CONTINUE);
                            }
                        }
                    }
                    return(STATUS_ABORT);
                }
    
                return(CheckForNMAPCommError(ccode));
            }

            return(STATUS_NMAP_COMM_ERROR);
        }

        if (ConnWrite(session->client.conn, ".TEXT] {0}\r\n", strlen(".TEXT] {0}\r\n")) != -1) {
            return(STATUS_CONTINUE);
        }
    }

    return(STATUS_ABORT);
}

static long 
FetchFlagResponderBodyPartTextPartial(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    FetchFlagStruct *flag = (FetchFlagStruct *)param3;
    long ccode;
    long count;
    unsigned long i;
    long resultLen;
    unsigned long textStart;
    unsigned long textLen;

    if (ConnWriteF(session->client.conn, "BODY[%lu", flag->bodyPart.part[0]) != -1) {
        for (i = 1; i < flag->bodyPart.depth; i++) {
            if (ConnWriteF(session->client.conn, ".%lu", flag->bodyPart.part[i]) != -1) {
                continue;
            }

            return(STATUS_ABORT);
        }

        if (HasPart(FetchRequest->messageDetail.mimeInfo, &(flag->bodyPart)) && flag->bodyPart.isMessage) {
            textStart = flag->bodyPart.partOffset + flag->bodyPart.messageHeaderLen;
            textLen = flag->bodyPart.partLen - flag->bodyPart.messageHeaderLen;
            if (flag->partial.start < textLen) {
                if ((flag->partial.start + flag->partial.len) < textLen) {
                    /* all in range */
                    resultLen = flag->partial.len;
                } else {
                    /* len out of range */
                    resultLen = textLen - flag->partial.start;
                }
                if (NMAPSendCommandF(session->store.conn, "READ %llx %lu %lu\r\n", FetchRequest->message->guid, textStart + flag->partial.start, resultLen) != -1) {
                    if ((ccode = NMAPReadPropertyValueLength(session->store.conn, "nmap.document", &count)) == 2001) {
                        if (ConnWriteF(session->client.conn, ".TEXT]<%lu> {%lu}\r\n", flag->partial.start, count) != -1) {
                            if (ConnReadToConn(session->store.conn, session->client.conn, count) == count) {
                                if (NMAPReadCrLf(session->store.conn) == 2) {
                                    return(STATUS_CONTINUE);
                                }
                            }
                        }

                        return(STATUS_ABORT);
                    }

                    return(CheckForNMAPCommError(ccode));
                }

                return(STATUS_NMAP_COMM_ERROR);
            }

            /* start out of range */
        }

        if (ConnWriteF(session->client.conn, ".TEXT]<%lu> {0}\r\n", flag->partial.start) != -1) {
            return(STATUS_CONTINUE);
        }
    }
    return(STATUS_ABORT);
}

static long 
FetchFlagResponderBodyPartHeader(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    FetchFlagStruct *flag = (FetchFlagStruct *)param3;
    long ccode;
    long count;
    unsigned long i;

    if (ConnWriteF(session->client.conn, "BODY[%lu", flag->bodyPart.part[0]) != -1) {
        for (i = 1; i < flag->bodyPart.depth; i++) {
            if (ConnWriteF(session->client.conn, ".%lu", flag->bodyPart.part[i]) != -1) {
                continue;
            }
            return(STATUS_ABORT);
        }

        if (HasPart(FetchRequest->messageDetail.mimeInfo, &(flag->bodyPart)) && flag->bodyPart.isMessage ) {
            if (NMAPSendCommandF(session->store.conn, "READ %llx %lu %lu\r\n", FetchRequest->message->guid, flag->bodyPart.partOffset, flag->bodyPart.messageHeaderLen) != -1) {
                if ((ccode = NMAPReadPropertyValueLength(session->store.conn, "nmap.document", &count)) == 2001) {
                    if (ConnWriteF(session->client.conn, ".HEADER] {%lu}\r\n", count) != -1) {
                        if (ConnReadToConn(session->store.conn, session->client.conn, count) == count) {
                            if (NMAPReadCrLf(session->store.conn) == 2) {
                                return(STATUS_CONTINUE);
                            }
                        }
                    }

                    return(STATUS_ABORT);
                }

                return(CheckForNMAPCommError(ccode));
            }

            return(STATUS_NMAP_COMM_ERROR);
        }
        if (ConnWrite(session->client.conn, ".HEADER] {0}\r\n", strlen(".HEADER] {0}\r\n")) != -1) {
            return(STATUS_CONTINUE);
        }
    }
    return(STATUS_ABORT);
}

static long 
FetchFlagResponderBodyPartHeaderPartial(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    FetchFlagStruct *flag = (FetchFlagStruct *)param3;
    long ccode;
    long count;
    unsigned long i;
    long resultLen;

    if (ConnWriteF(session->client.conn, "BODY[%lu", flag->bodyPart.part[0]) != -1) {
        for (i = 1; i < flag->bodyPart.depth; i++) {
            if (ConnWriteF(session->client.conn, ".%lu", flag->bodyPart.part[i]) != -1) {
                continue;
            }
            return(STATUS_ABORT);
        }
        if (HasPart(FetchRequest->messageDetail.mimeInfo, &(flag->bodyPart)) && flag->bodyPart.isMessage) {
            if (flag->partial.start < flag->bodyPart.messageHeaderLen) {
                if ((flag->partial.start + flag->partial.len) < flag->bodyPart.messageHeaderLen) {
                    /* all in range */
                    resultLen = flag->partial.len;
                } else {
                    /* len out of range */
                    resultLen = flag->bodyPart.messageHeaderLen - flag->partial.start;
                }
                if (NMAPSendCommandF(session->store.conn, "READ %llx %lu %lu\r\n", FetchRequest->message->guid, flag->bodyPart.partOffset + flag->partial.start, resultLen) != -1) {
                    if ((ccode = NMAPReadPropertyValueLength(session->store.conn, "nmap.document", &count)) == 2001) {
                        if (ConnWriteF(session->client.conn, ".HEADER]<%lu> {%lu}\r\n", flag->partial.start, count) != -1) {
                            if (ConnReadToConn(session->store.conn, session->client.conn, count) == count) {
                                if (NMAPReadCrLf(session->store.conn) == 2) {
                                    return(STATUS_CONTINUE);
                                }
                            }
                        }
                        return(STATUS_ABORT);
                    }
                    return(CheckForNMAPCommError(ccode));
                }
                return(STATUS_NMAP_COMM_ERROR);
            }
            /* start out of range */
        }
        if (ConnWriteF(session->client.conn, ".HEADER]<%lu> {0}\r\n", flag->partial.start) != -1) {
            return(STATUS_CONTINUE);
        }
    }
    return(STATUS_ABORT);
}

static long 
FetchFlagResponderBodyPartHeaderFields(void *param1, void *param2, void *param3)
{
    FetchFlagStruct *flag = (FetchFlagStruct *)param3;
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
  
    long ccode;
    unsigned long j;
    unsigned char *fullHeader;
    long fullHeaderLen;
    unsigned char *resultHeader;
    unsigned long resultHeaderLen;

    if (ConnWriteF(session->client.conn, "BODY[%lu", flag->bodyPart.part[0]) != -1) {
        for (j = 1; j < flag->bodyPart.depth; j++) {
            if (ConnWriteF(session->client.conn, ".%lu", flag->bodyPart.part[j]) != -1) {
                continue;
            }
            return(STATUS_ABORT);
        }
    } else {
        return(STATUS_ABORT);
    }

    if (!(flag->fieldList.skip)) {
        ccode = ConnWrite(session->client.conn, ".HEADER.FIELDS (", strlen(".HEADER.FIELDS ("));
    } else {
        ccode = ConnWrite(session->client.conn, ".HEADER.FIELDS.NOT (", strlen(".HEADER.FIELDS.NOT ("));
    }

    if (ccode != -1) {
        for (j = 0; j < flag->fieldList.count - 1; j++) {
            if (ConnWriteF(session->client.conn, "\"%s\" ", flag->fieldList.field[j]) != -1) {
                continue;
            }
            return(STATUS_ABORT);
        } 
    } else {
        return(STATUS_ABORT);
    }

    if (ConnWriteF(session->client.conn, "\"%s\")]", flag->fieldList.field[j]) != -1) {
        ;
    } else {
        return(STATUS_ABORT);
    }

    if (HasPart(FetchRequest->messageDetail.mimeInfo, &(flag->bodyPart)) && flag->bodyPart.isMessage) {
        if (NMAPSendCommandF(session->store.conn, "READ %llx %lu %lu\r\n", FetchRequest->message->guid, flag->bodyPart.partOffset, flag->bodyPart.messageHeaderLen) != -1) {
            ccode = NMAPReadResponse(session->store.conn, session->store.response, sizeof(session->store.response), TRUE);
            if (ccode == 2001) {
                fullHeaderLen = atoi(session->store.response);
                fullHeader = MemMalloc(fullHeaderLen + 1);
                if (fullHeader) {
                    if (NMAPReadCount(session->store.conn, fullHeader, fullHeaderLen) == fullHeaderLen) {
                        if (NMAPReadCrLf(session->store.conn) == 2) {
                            fullHeader[fullHeaderLen] = '0';
                            resultHeader = GetSubHeader(fullHeader, fullHeaderLen, flag->fieldList.field, flag->fieldList.count, flag->fieldList.skip, &resultHeaderLen);
                            MemFree(fullHeader);
                            if (resultHeader) {
                                if (!flag->isPartial) {
                                    if (resultHeaderLen > 0) {
                                        if (ConnWriteF(session->client.conn, " {%lu}\r\n", resultHeaderLen) != -1) {
                                            if (ConnWrite(session->client.conn, resultHeader, resultHeaderLen) != -1) {
                                                MemFree(resultHeader);
                                                return(STATUS_CONTINUE);
                                            }
                                        }
                                        MemFree(resultHeader);
                                        return(STATUS_ABORT);
                                    }

                                    if (ConnWriteF(session->client.conn, " {0}\r\n", sizeof(" {0}\r\n") - 1) != -1) {
                                        MemFree(resultHeader);
                                        return(STATUS_CONTINUE);
                                    }
                                    MemFree(resultHeader);
                                    return(STATUS_ABORT);
                                }

                                if (resultHeaderLen > 0) {
                                    if (flag->partial.start < resultHeaderLen) {
                                        if ((flag->partial.start + flag->partial.len) < resultHeaderLen) {
                                            if (ConnWriteF(session->client.conn, "<%lu> {%lu}\r\n", flag->partial.start, flag->partial.len) != -1) {
                                                if (ConnWrite(session->client.conn, resultHeader + flag->partial.start, flag->partial.len) != -1) {
                                                    MemFree(resultHeader);
                                                    return(STATUS_CONTINUE);
                                                }
                                            }
                                            MemFree(resultHeader);
                                            return(STATUS_ABORT);
                                        }


                                        if (ConnWriteF(session->client.conn, "<%lu> {%lu}\r\n", flag->partial.start, resultHeaderLen - flag->partial.start) != -1) {
                                            if (ConnWrite(session->client.conn, resultHeader + flag->partial.start, resultHeaderLen - flag->partial.start) != -1) {
                                                MemFree(resultHeader);
                                                return(STATUS_CONTINUE);
                                            }
                                        }
                                        MemFree(resultHeader);
                                        return(STATUS_ABORT);
                                    }
                                }

                                if (ConnWriteF(session->client.conn, "<%lu> {0}\r\n", flag->partial.start) != -1) {
                                    MemFree(resultHeader);
                                    return(STATUS_CONTINUE);
                                }
                                MemFree(resultHeader);
                                return(STATUS_ABORT);
                            }
                    
                            return(STATUS_MEMORY_ERROR);
                        }
                    }

                    MemFree(fullHeader);
                    return(STATUS_ABORT);
                }
                return(STATUS_MEMORY_ERROR);
            }
            return(CheckForNMAPCommError(ccode));
        }
        return(STATUS_NMAP_COMM_ERROR);
    }

    if (!flag->isPartial) {
        if (ConnWrite(session->client.conn, " {0}\r\n", strlen(" {0}\r\n")) != -1) {
            return(STATUS_CONTINUE);
        }
        return(STATUS_ABORT);
    }

    if (ConnWriteF(session->client.conn, "<%lu> {0}\r\n", flag->partial.start) != -1) {
        return(STATUS_CONTINUE);
    }
    return(STATUS_ABORT);
}

static long 
FetchFlagResponderBodyPartMime(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    FetchFlagStruct *flag = (FetchFlagStruct *)param3;
    long ccode;
    long count;
    unsigned long i;

    if (ConnWriteF(session->client.conn, "BODY[%lu", flag->bodyPart.part[0]) != -1) {
        for (i = 1; i < flag->bodyPart.depth; i++) {
            if (ConnWriteF(session->client.conn, ".%lu", flag->bodyPart.part[i]) != -1) {
                continue;
            }
            return(STATUS_ABORT);
        }
        if (HasPart(FetchRequest->messageDetail.mimeInfo, &(flag->bodyPart))) {
            if (NMAPSendCommandF(session->store.conn, "READ %llx %lu %lu\r\n", FetchRequest->message->guid, flag->bodyPart.mimeOffset, flag->bodyPart.mimeLen) != -1) {
                if ((ccode = NMAPReadPropertyValueLength(session->store.conn, "nmap.document", &count)) == 2001) {
                    if (ConnWriteF(session->client.conn, ".MIME] {%lu}\r\n", count) != -1) {
                        if (ConnReadToConn(session->store.conn, session->client.conn, count) == count) {
                            if (NMAPReadCrLf(session->store.conn) == 2) {
                                return(STATUS_CONTINUE);
                            }
                        }
                    }
                    return(STATUS_ABORT);
                }
                return(CheckForNMAPCommError(ccode));
            }
            return(STATUS_NMAP_COMM_ERROR);
        }

        if (ConnWrite(session->client.conn, ".MIME] {0}\r\n", strlen(".MIME] {0}\r\n")) != -1) {
            return(STATUS_CONTINUE);
        }
    }
    return(STATUS_ABORT);

}

static long 
FetchFlagResponderBodyPartMimePartial(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1;
    FetchStruct *FetchRequest = (FetchStruct *)param2;
    FetchFlagStruct *flag = (FetchFlagStruct *)param3;
    long ccode;
    long count;
    unsigned long i;

    if (ConnWriteF(session->client.conn, "BODY[%lu", flag->bodyPart.part[0]) != -1) {
        for (i = 1; i < flag->bodyPart.depth; i++) {
            if (ConnWriteF(session->client.conn, ".%lu", flag->bodyPart.part[i]) != -1) {
                continue;
            }
            return(STATUS_ABORT);
        }
        if (HasPart(FetchRequest->messageDetail.mimeInfo, &(flag->bodyPart)) && (flag->partial.start < flag->bodyPart.mimeLen)) {
            if ((flag->partial.start + flag->partial.len) < flag->bodyPart.mimeLen) {
                count = flag->partial.len;
            } else {
                count = flag->bodyPart.mimeLen - flag->partial.start;
            }
            if (NMAPSendCommandF(session->store.conn, "READ %llx %lu %lu\r\n", FetchRequest->message->guid, flag->bodyPart.mimeOffset + flag->partial.start, count) != -1) {
                if ((ccode = NMAPReadPropertyValueLength(session->store.conn, "nmap.document", &count)) == 2001) {
                    if (ConnWriteF(session->client.conn, ".MIME] {%lu}\r\n", count) != -1) {
                        if (ConnReadToConn(session->store.conn, session->client.conn, count) == count) {
                            if (NMAPReadCrLf(session->store.conn) == 2) {
                                return(STATUS_CONTINUE);
                            }
                        }
                    }
                    return(STATUS_ABORT);
                }
                return(CheckForNMAPCommError(ccode));
            }
            return(STATUS_NMAP_COMM_ERROR);
        }

        if (ConnWrite(session->client.conn, ".MIME] {0}\r\n", strlen(".MIME] {0}\r\n")) != -1) {
            return(STATUS_CONTINUE);
        }
    }
    return(STATUS_ABORT);

}

static long 
FetchFlagResponderBodyPart(void *param1, void *param2, void *param3)
{
    FetchFlagStruct *flag = (FetchFlagStruct *)param3;
    return(flag->bodyPart.responder(param1, param2, param3));
}

static long
FetchFlagResponderFast(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1; 
    long ccode;

    if ((ccode = FetchFlagResponderFlags(param1, param2, param3)) == STATUS_CONTINUE) {
        if ((ccode = ConnWrite(session->client.conn, " ", 1)) != -1) {
            if ((ccode = FetchFlagResponderInternalDate(param1, param2, param3)) == STATUS_CONTINUE) {
                if ((ccode = ConnWrite(session->client.conn, " ", 1)) != -1) {
                    return(FetchFlagResponderRfc822Size(param1, param2, param3));
                }
            }
        }
    }
    return(ccode);
}

static long 
FetchFlagResponderFull(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1; 
    long ccode;

    if ((ccode = FetchFlagResponderFlags(param1, param2, param3)) == STATUS_CONTINUE) {
        if ((ccode = ConnWrite(session->client.conn, " ", 1)) != -1) {
            if ((ccode = FetchFlagResponderInternalDate(param1, param2, param3)) == STATUS_CONTINUE) {
                if ((ccode = ConnWrite(session->client.conn, " ", 1)) != -1) {
                    if ((ccode = FetchFlagResponderRfc822Size(param1, param2, param3)) == STATUS_CONTINUE) {
                        if ((ccode = ConnWrite(session->client.conn, " ", 1)) != -1) {
                            if ((ccode = FetchFlagResponderEnvelope(param1, param2, param3)) == STATUS_CONTINUE) {
                                if ((ccode = ConnWrite(session->client.conn, " ", 1)) != -1) {
                                    return(FetchFlagResponderBodyStructure(param1, param2, param3));
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    return(ccode);
}

static long 
FetchFlagResponderAll(void *param1, void *param2, void *param3)
{
    ImapSession *session = (ImapSession *)param1; 
    long ccode;

    if ((ccode = FetchFlagResponderFlags(param1, param2, param3)) == STATUS_CONTINUE) {
        if ((ccode = ConnWrite(session->client.conn, " ", 1)) != -1) {
            if ((ccode = FetchFlagResponderInternalDate(param1, param2, param3)) == STATUS_CONTINUE) {
                if ((ccode = ConnWrite(session->client.conn, " ", 1)) != -1) {
                    if ((ccode = FetchFlagResponderRfc822Size(param1, param2, param3)) == STATUS_CONTINUE) {
                        if ((ccode = ConnWrite(session->client.conn, " ", 1)) != -1) {
                            return(FetchFlagResponderEnvelope(param1, param2, param3));
                        }
                    }
                }
            }
        }
    }
    return(ccode);
}

static unsigned long
ParseFetchFlagPartial(unsigned char **parentPtr, FetchFlagStruct *flag)
{
    unsigned char *ptr;

    ptr = *parentPtr;
    if (isdigit(*ptr)) {
        flag->partial.start = atol(ptr);
        do {
            ptr++;
            if (isdigit(*ptr)) {
                continue;
            }
            break;
        } while(TRUE);

        if (*ptr == '.') {
            ptr++;
            if (isdigit(*ptr)) {
                flag->partial.len = atol(ptr);
                do {
                    ptr++;
                } while(isdigit(*ptr));
                if (*ptr == '>') {
                    flag->isPartial = TRUE;
                    *parentPtr = ptr + 1;
                    return(STATUS_CONTINUE);
                }
            }
        }
    }

    return(F_PARSE_ERROR);
}

__inline static unsigned long
ParseHeaderFields(unsigned char **parentPtr, FetchFlagStruct *flag)
{
    unsigned char *start;
    unsigned char *end;
    unsigned char *current;
    unsigned char *fieldBuffer;
    unsigned long fieldBufferLen;
    unsigned long spaceCount;
    unsigned char len;
    unsigned long fieldCount;
    unsigned long ccode;

    end = strchr(*parentPtr, ')');
    if (end) {
        fieldBufferLen = end - *parentPtr;
        fieldBuffer = MemMalloc(fieldBufferLen + 1);
        if (fieldBuffer) {
            flag->hasAllocated |= ALLOCATED_FIELDS;
            memcpy(fieldBuffer, *parentPtr, fieldBufferLen);
            fieldBuffer[fieldBufferLen] = '\0';

            start = fieldBuffer;
            end = fieldBuffer + fieldBufferLen;
            current = start;

            spaceCount = 0;
            do {
                if (*current != ' ') {
                    current++;
                    continue;
                }
                
                spaceCount++;
                current++;
            } while (current < end);

            flag->fieldList.fieldRequest = fieldBuffer;
            if (spaceCount < FETCH_FIELD_ALLOC_THRESHOLD) {
                flag->fieldList.field = &(flag->fieldList.fieldEmbedded[0]);
            } else {
                flag->fieldList.field = MemCalloc((spaceCount + 1), sizeof(unsigned char *));
                if (flag->fieldList.field) {
                    flag->hasAllocated |= ALLOCATED_FIELD_POINTERS;
                } else {
                    return(F_SYSTEM_ERROR);
                }
            }

            fieldCount = 0;
            current = start;

            do {
                if (*current == '"') {
                    current++;
                    start = current;
                    do {
                        if (current < end) {
                            if (*current != '"') {
                                current++;
                                continue;
                            }

                            if (*(current - 1) != '\\') {
                                len = current - start;
                                *current = '\0';
                                current++;
                                break;
                            }

                            current++;
                            continue;
                        }

                        return(F_PARSE_ERROR);
                    } while (TRUE);
                } else {
                    start = current;
                    do {
                        if (current < end) {
                            if (*current != ' ') {
                                current++;
                                continue;
                            }
                        }

                        len = current - start;
                            
                        break;
                    } while (TRUE);
                }

                /* we've got a header name */
                if (len > 0) {
                    flag->fieldList.field[fieldCount] = start;
                    fieldCount++;
                    flag->fieldList.count = fieldCount;

                    if (current < end) {
                        if (*current == ' ') {
                            *current = '\0';
                            current++;
                            continue;
                        }

                        return(F_PARSE_ERROR);
                    }
                    
                    break;
                }
                
                return(F_PARSE_ERROR);
            } while (current < end);

            /* we have fields; lets go back to the original buffer and see what comes after the fields */
            current = *parentPtr + fieldBufferLen + 1;
            if (*current == ']') {
                current++;
                if (*current != '<') {
                    flag->isPartial = FALSE;
                    *parentPtr = current;
                    return(0);
                }

                current++;
                ccode = ParseFetchFlagPartial(&current, flag);
                if (ccode == 0) {
                    *parentPtr = current;
                    return(0);
                }

                return(ccode);
            }
            
            return(F_PARSE_ERROR);
        }
        return(F_SYSTEM_ERROR);
    }

    return(F_PARSE_ERROR);
}

__inline static unsigned long
ParseFetchFlagHeaderFields(unsigned char **parentPtr, FetchFlagStruct *flag)
{
    return(ParseHeaderFields(parentPtr, flag));
}

__inline static unsigned long
ParseFetchFlagHeaderFieldsNot(unsigned char **parentPtr, FetchFlagStruct *flag)
{
    flag->fieldList.skip = TRUE;
    return(ParseHeaderFields(parentPtr, flag));
}

static FetchFlag FetchFlagsSection[] = {
    {"TEXT]", sizeof("TEXT]") - 1, F_BODY_TEXT, NULL, FetchFlagResponderBodyPartText},
    {"TEXT]<", sizeof("TEXT]<") - 1, F_BODY_TEXT, ParseFetchFlagPartial, FetchFlagResponderBodyPartTextPartial},
    {"MIME]", sizeof("MIME]") - 1, F_BODY_MIME, NULL, FetchFlagResponderBodyPartMime},
    {"MIME]<", sizeof("MIME]<") - 1, F_BODY_MIME, ParseFetchFlagPartial, FetchFlagResponderBodyPartMimePartial},
    {"HEADER]", sizeof("HEADER]") - 1, F_BODY_HEADER, NULL, FetchFlagResponderBodyPartHeader},
    {"HEADER]<", sizeof("HEADER]<") - 1, F_BODY_HEADER, ParseFetchFlagPartial, FetchFlagResponderBodyPartHeaderPartial},
    {"HEADER.FIELDS (", sizeof("HEADER.FIELDS (") - 1, F_BODY_HEADER_FIELDS, ParseFetchFlagHeaderFields, FetchFlagResponderBodyPartHeaderFields},
    {"HEADER.FIELDS.NOT (", sizeof("HEADER.FIELDS.NOT (") - 1, F_BODY_HEADER_FIELDS_NOT, ParseFetchFlagHeaderFieldsNot, FetchFlagResponderBodyPartHeaderFields},
    {NULL, 0, 0, NULL, NULL}
};

static unsigned long
ParseFetchFlagBodyPart(unsigned char **parentPtr, FetchFlagStruct *flag)
{
    unsigned char *ptr;
    unsigned long *tmpPart;
    long sectionId;
    unsigned long ccode;

    ptr = *parentPtr;

    flag->bodyPart.part = (unsigned long *)&(flag->bodyPart.partEmbedded);
    do {
        if (isdigit(*ptr)) {
            if (flag->bodyPart.depth < PART_DEPTH_ALLOC_THRESHOLD) {
                /* No allocation needed; use space in the structure */
                ;
            } else if (flag->bodyPart.depth > PART_DEPTH_ALLOC_THRESHOLD) {
                /* Grow the existing allocated structure */
                tmpPart = MemRealloc(flag->bodyPart.part, sizeof(unsigned long) * (flag->bodyPart.depth + 1));
                if (tmpPart) {
                    flag->bodyPart.part = tmpPart;
                } else {
                    return(F_SYSTEM_ERROR);
                }
            } else {
                /* switch from embedded space to allocated space */
                tmpPart = MemMalloc(sizeof(unsigned long) * (PART_DEPTH_ALLOC_THRESHOLD + 1));
                if (tmpPart) {
                    flag->hasAllocated |= ALLOCATED_PART_NUMBERS;
                    /* copy the data from embedded array to allocated array */
                    memcpy(tmpPart, flag->bodyPart.part, sizeof(unsigned long) * PART_DEPTH_ALLOC_THRESHOLD);
                    flag->bodyPart.part = tmpPart;
                } else {
                    return(F_SYSTEM_ERROR);
                }
            }

            flag->bodyPart.part[flag->bodyPart.depth] = atoi(ptr);
            flag->bodyPart.depth++;

            do {
                ptr++;
            } while (isdigit(*ptr));

            if (*ptr == '.') {
                ptr++;
                continue;
            }
            
            if (*ptr == ']') {
                ptr++;
                if (*ptr != '<') {
                    flag->bodyPart.responder = FetchFlagResponderBodyPartNumber;
                    *parentPtr = ptr;
                    return(0);
                }

                ptr++;
                ccode = ParseFetchFlagPartial(&ptr, flag);
                if (ccode == 0) {
                    flag->bodyPart.responder = FetchFlagResponderBodyPartNumberPartial;
                    *parentPtr = ptr;
                    return(0);
                }

                return(ccode);
            }
            
            return(F_PARSE_ERROR);
        }

        sectionId = BongoKeywordBegins(FetchFlagsSectionIndex, ptr);
        if (sectionId != -1) {
            ptr += FetchFlagsSection[sectionId].nameLen;
            flag->bodyPart.responder = FetchFlagsSection[sectionId].responder;
            if (FetchFlagsSection[sectionId].parser == NULL) {
                *parentPtr = ptr;
                return(0);
            }

            ccode = FetchFlagsSection[sectionId].parser(&ptr, flag);
            if (ccode == 0) {
                *parentPtr = ptr;
                return(0);
            }
            
            return(ccode);
        }

        return(F_PARSE_ERROR);
    } while(TRUE);
}


#define FETCH_ATT_FLAGS \
    {"UID", sizeof("UID") - 1, F_UID, NULL, FetchFlagResponderUid}, \
    {"FLAGS", sizeof("FLAGS") - 1, F_FLAGS, NULL, FetchFlagResponderFlags}, \
    {"ENVELOPE", sizeof("ENVELOPE") - 1, F_ENVELOPE, NULL, FetchFlagResponderEnvelope}, \
    {"XSENDER", sizeof("XSENDER") - 1, F_XSENDER, NULL, FetchFlagResponderXSender}, \
    {"INTERNALDATE", sizeof("INTERNALDATE") - 1, F_INTERNALDATE, NULL, FetchFlagResponderInternalDate}, \
    {"BODYSTRUCTURE", sizeof("BODYSTRUCTURE") - 1, F_BODYSTRUCTURE, NULL, FetchFlagResponderBodyStructure}, \
    {"RFC822.TEXT", sizeof("RFC822.TEXT") - 1, F_RFC822_TEXT | F_BODY_SEEN, NULL, FetchFlagResponderRfc822Text}, \
    {"RFC822.SIZE", sizeof("RFC822.SIZE") - 1, F_RFC822_SIZE, NULL, FetchFlagResponderRfc822Size}, \
    {"RFC822.HEADER", sizeof("RFC822.HEADER") - 1, F_RFC822_HEADER, NULL, FetchFlagResponderRfc822Header}, \
    {"RFC822", sizeof("RFC822") - 1, F_RFC822_BOTH | F_BODY_SEEN, NULL, FetchFlagResponderRfc822}, \
    {"BODY", sizeof("BODY") - 1, F_BODY, NULL, FetchFlagResponderBody}, \
    {"BODY[HEADER]", sizeof("BODY[HEADER]") - 1, F_BODY_HEADER | F_BODY_SEEN, NULL, FetchFlagResponderBodyHeader}, \
    {"BODY[HEADER]<", sizeof("BODY[HEADER]<") - 1, F_BODY_HEADER_PARTIAL | F_BODY_SEEN, ParseFetchFlagPartial, FetchFlagResponderBodyHeaderPartial}, \
    {"BODY[TEXT]", sizeof("BODY[TEXT]") - 1, F_BODY_TEXT | F_BODY_SEEN, NULL, FetchFlagResponderBodyText}, \
    {"BODY[TEXT]<", sizeof("BODY[TEXT]<") - 1, F_BODY_TEXT_PARTIAL | F_BODY_SEEN, ParseFetchFlagPartial, FetchFlagResponderBodyTextPartial}, \
    {"BODY[]", sizeof("BODY[]") - 1, F_BODY_BOTH | F_BODY_SEEN, NULL, FetchFlagResponderBodyBoth}, \
    {"BODY[]<", sizeof("BODY[]<") - 1, F_BODY_BOTH_PARTIAL | F_BODY_SEEN, ParseFetchFlagPartial, FetchFlagResponderBodyBothPartial}, \
    {"BODY[MIME]", sizeof("BODY[MIME]") - 1, F_BODY_MIME | F_BODY_SEEN, NULL, FetchFlagResponderBodyMime}, \
    {"BODY[MIME]<", sizeof("BODY[MIME]<") - 1, F_BODY_MIME_PARTIAL | F_BODY_SEEN, ParseFetchFlagPartial, FetchFlagResponderBodyMimePartial}, \
    {"BODY[HEADER.FIELDS (", sizeof("BODY[HEADER.FIELDS (") - 1, F_BODY_HEADER_FIELDS | F_BODY_SEEN, ParseFetchFlagHeaderFields, FetchFlagResponderBodyHeaderFields}, \
    {"BODY[HEADER.FIELDS.NOT (", sizeof("BODY[HEADER.FIELDS.NOT (") - 1, F_BODY_HEADER_FIELDS_NOT | F_BODY_SEEN, ParseFetchFlagHeaderFieldsNot, FetchFlagResponderBodyHeaderFields}, \
    {"BODY[", sizeof("BODY[") - 1, F_BODY_PART | F_BODY_SEEN, ParseFetchFlagBodyPart, FetchFlagResponderBodyPart}, \
    {"BODY.PEEK[TEXT]", sizeof("BODY.PEEK[TEXT]") - 1, F_BODY_TEXT, NULL, FetchFlagResponderBodyText}, \
    {"BODY.PEEK[TEXT]<", sizeof("BODY.PEEK[TEXT]<") - 1, F_BODY_TEXT_PARTIAL, ParseFetchFlagPartial, FetchFlagResponderBodyTextPartial}, \
    {"BODY.PEEK[HEADER]", sizeof("BODY.PEEK[HEADER]") - 1, F_BODY_HEADER, NULL, FetchFlagResponderBodyHeader}, \
    {"BODY.PEEK[HEADER]<", sizeof("BODY.PEEK[HEADER]<") - 1, F_BODY_HEADER | F_BODY_HEADER_PARTIAL, ParseFetchFlagPartial, FetchFlagResponderBodyHeaderPartial}, \
    {"BODY.PEEK[]", sizeof("BODY.PEEK[]") - 1, F_BODY_BOTH, NULL, FetchFlagResponderBodyBoth}, \
    {"BODY.PEEK[]<", sizeof("BODY.PEEK[]<") - 1, F_BODY_BOTH | F_BODY_BOTH_PARTIAL, ParseFetchFlagPartial, FetchFlagResponderBodyBothPartial}, \
    {"BODY.PEEK[MIME]", sizeof("BODY.PEEK[MIME]") - 1, F_BODY_MIME, NULL, FetchFlagResponderBodyMime}, \
    {"BODY.PEEK[MIME]<", sizeof("BODY.PEEK[MIME]<") - 1, F_BODY_MIME | F_BODY_MIME_PARTIAL, ParseFetchFlagPartial, FetchFlagResponderBodyMimePartial}, \
    {"BODY.PEEK[HEADER.FIELDS (", sizeof("BODY.PEEK[HEADER.FIELDS (") - 1, F_BODY_HEADER_FIELDS, ParseFetchFlagHeaderFields, FetchFlagResponderBodyHeaderFields}, \
    {"BODY.PEEK[HEADER.FIELDS.NOT (", sizeof("BODY.PEEK[HEADER.FIELDS.NOT (") - 1, F_BODY_HEADER_FIELDS_NOT, ParseFetchFlagHeaderFieldsNot, FetchFlagResponderBodyHeaderFields}, \
    {"BODY.PEEK[", sizeof("BODY.PEEK[") - 1, F_BODY_PART, ParseFetchFlagBodyPart, FetchFlagResponderBodyPart},

static FetchFlag FetchFlagsSolo[] = {
    {"FAST", sizeof("FAST") - 1, F_FLAGS | F_INTERNALDATE | F_RFC822_SIZE, NULL, FetchFlagResponderFast},
    {"FULL", sizeof("FULL") - 1, F_FLAGS | F_INTERNALDATE | F_RFC822_SIZE | F_ENVELOPE | F_BODYSTRUCTURE, NULL, FetchFlagResponderFull},
    {"ALL", sizeof("ALL") - 1, F_FLAGS | F_INTERNALDATE | F_RFC822_SIZE | F_ENVELOPE, NULL, FetchFlagResponderAll},
    FETCH_ATT_FLAGS
    {NULL, 0, 0, NULL, NULL}
};

static FetchFlag FetchFlagsAtt[] = {
    FETCH_ATT_FLAGS
    {NULL, 0, 0, NULL, NULL}
};

__inline static void
RemoveLastFlag(FetchStruct *FetchRequest)
{
    FetchRequest->flagCount--;
}

__inline static FetchFlagStruct *
AddNewFlag(FetchStruct *FetchRequest, FetchResponder responder)
{
    FetchFlagStruct *newFlag;
    FetchFlagStruct *tmpFlag;

    if (FetchRequest->flagCount < FETCH_FLAG_ALLOC_THRESHOLD) {
        FetchRequest->flag = FetchRequest->flagEmbedded;
        newFlag = &(FetchRequest->flag[FetchRequest->flagCount]);
        newFlag->responder = responder;
        FetchRequest->flagCount++;
        return(newFlag);
    }

    if (FetchRequest->flagCount > FETCH_FLAG_ALLOC_THRESHOLD) {
        tmpFlag = MemRealloc(FetchRequest->flag, sizeof(FetchFlagStruct) * (FetchRequest->flagCount + 1));
        if (tmpFlag) {
            FetchRequest->flag = tmpFlag;
            newFlag = &(FetchRequest->flag[FetchRequest->flagCount]);
            memset(newFlag, 0, sizeof(FetchFlagStruct));
            newFlag->responder = responder;
            FetchRequest->flagCount++;
            return(newFlag);
        }

        return(NULL);
    }

    tmpFlag = MemMalloc(sizeof(FetchFlagStruct) * (FETCH_FLAG_ALLOC_THRESHOLD + 1));
    if (tmpFlag) {
        FetchRequest->hasAllocated |= ALLOCATED_FLAGS;
        memcpy(tmpFlag, FetchRequest->flagEmbedded, sizeof(FetchFlagStruct) *  FETCH_FLAG_ALLOC_THRESHOLD);
        FetchRequest->flag = tmpFlag;
        newFlag = &(FetchRequest->flag[FetchRequest->flagCount]);
        memset(newFlag, 0, sizeof(FetchFlagStruct));
        newFlag->responder = responder;
        FetchRequest->flagCount++;
        return(newFlag);
    }

    return(NULL);
}

__inline static long
ParseFetchArguments(ImapSession *session, unsigned char *ptr, FetchStruct *FetchRequest)
{
    unsigned char *Items;
    long allFlags = FetchRequest->flags;
    long flagID;
    FetchFlag flagInfo;
    FetchFlagStruct *currentFlag;
    long ccode;

    /* Grab the range */
    ccode = GrabArgument(session, &ptr, &FetchRequest->messageSet);
    if (ccode == STATUS_CONTINUE) {
        if (FetchRequest->messageSet) {
            if (*ptr == ' ') {
                *ptr++;
                if (*ptr != '\0') {
                    /* parse the fetch flags */
                    if (*ptr == '(') {
                        /* we have parens so we can have one or more 'fetch-att' flags */
                        ccode = GrabArgument(session, &ptr, &Items);
                        if (ccode == STATUS_CONTINUE) {
                            ptr = Items - 1;
                            do {
                                ptr++;
                                flagID = BongoKeywordBegins(FetchFlagsAttIndex, ptr);
                                if (flagID != -1) {
                                    flagInfo = FetchFlagsAtt[flagID];
                                    ptr += flagInfo.nameLen;
                                    currentFlag = AddNewFlag(FetchRequest, flagInfo.responder);
                                    if (currentFlag) {
                                        if (flagInfo.parser == NULL) {
                                            /* make sure uid only gets added once; it gets added automatically for uid fetch requests */
                                            if ((!(flagInfo.value & F_UID)) || (!(allFlags & F_UID))) {
                                                allFlags |= flagInfo.value;
                                            } else {
                                                RemoveLastFlag(FetchRequest);
                                            }
                                            continue;
                                        }
                
                                        allFlags |= flagInfo.value;

                                        ccode = flagInfo.parser(&ptr, currentFlag);
                                        FetchRequest->hasAllocated |= currentFlag->hasAllocated;
                                        if (ccode == 0) {
                                            continue;
                                        }
                                        return(ccode);
                                    }
                                    return(F_SYSTEM_ERROR);
                                }
                                MemFree(Items);
                                return(F_PARSE_ERROR);
                            } while (*ptr == ' ');

                            if (*ptr == '\0') {
                                MemFree(Items);
                                return(allFlags);
                            }

                            MemFree(Items);
                        }

                        return(F_PARSE_ERROR);
                    }

                    /* there are no parens so there can only be one macro or one 'fetch-att' flag */
                    flagID = BongoKeywordBegins(FetchFlagsSoloIndex, ptr);
                    if (flagID != -1) {
                        flagInfo = FetchFlagsSolo[flagID];
                            
                        ptr += flagInfo.nameLen;
                        currentFlag = AddNewFlag(FetchRequest, flagInfo.responder);
                        if (currentFlag) {
                            if (flagInfo.parser == NULL) {
                                if (*ptr == '\0') {
                                    if ((!(flagInfo.value & F_UID)) || (!(allFlags & F_UID))) {
                                        allFlags |= flagInfo.value;
                                    } else {
                                        RemoveLastFlag(FetchRequest);
                                    }

                                    return(allFlags);
                                }

                                return(F_PARSE_ERROR);
                            }
                
                            allFlags |= flagInfo.value;

                            ccode = flagInfo.parser(&ptr, currentFlag);
                            FetchRequest->hasAllocated |= currentFlag->hasAllocated;
                            if (ccode == 0) {
                                if (*ptr == '\0') {
                                    return(allFlags);
                                }
                                return(F_PARSE_ERROR);
                            }

                            return(ccode);

                        }
                        return(F_SYSTEM_ERROR);
                    }
                }
            }
        }

        return(F_PARSE_ERROR);
    }

    return(ccode);
}

__inline static long
SetMessageSeenFlag(ImapSession *session, FetchStruct *FetchRequest)
{
    long ccode;
    unsigned long oldFlags;
    unsigned long newFlags;
    char *ptr;


    if (NMAPSendCommandF(session->store.conn, "FLAG %llx +%lu\r\n", FetchRequest->message->guid, (unsigned long)STORE_MSG_FLAG_SEEN) != -1) {
        ccode = NMAPReadResponse(session->store.conn, session->store.response, sizeof(session->store.response), TRUE);
        if (ccode == 1000) {
            if (!(FetchRequest->flags & F_FLAGS)) {
                /* This fetch request does allow flags responses, but we have to wait for later to send them to the client  */
                oldFlags = atol(session->store.response);
                ptr = strchr(session->store.response, ' ');
                if (ptr) {
                    newFlags = atol(ptr + 1);
                    RememberFlags(session->folder.selected.events.flags, FetchRequest->message->guid, newFlags);
                }
            }
            return(STATUS_CONTINUE);
        }
        return(CheckForNMAPCommError(ccode));
    }
    return(STATUS_NMAP_COMM_ERROR);
}

__inline static long
GetMessageHeader(ImapSession *session, FetchStruct *FetchRequest)
{
    int ccode;
    long count;
    
    /* Request the header */
    if (NMAPSendCommandF(session->store.conn, "READ %llx 0 %lu\r\n", FetchRequest->message->guid, FetchRequest->message->headerSize) != -1) {
        if ((ccode = NMAPReadPropertyValueLength(session->store.conn, "nmap.document", &count)) == 2001) {
            FetchRequest->message->headerSize = count;
            FetchRequest->messageDetail.header = MemMalloc(FetchRequest->message->headerSize + 1);
            if (FetchRequest->messageDetail.header) {
                if ((ccode = NMAPReadCount(session->store.conn, FetchRequest->messageDetail.header, FetchRequest->message->headerSize)) > 0) {
                    if ((unsigned long)ccode == FetchRequest->message->headerSize) {
                        if (NMAPReadCrLf(session->store.conn) == 2) {
                            FetchRequest->messageDetail.header[FetchRequest->message->headerSize] = '\0';
                            MakeRFC822Header(FetchRequest->messageDetail.header, &FetchRequest->message->headerSize);
                            return(0);
                        }
                    }
                }
            }    

            return(STATUS_ABORT);            
        }

        return(CheckForNMAPCommError(ccode));
    }
    return(STATUS_NMAP_COMM_ERROR);
}

__inline static long
GetMimeInfo(ImapSession *session, FetchStruct *FetchRequest)
{
    long ccode;

    if (FetchRequest->messageDetail.mimeInfo == NULL) {
        FetchRequest->messageDetail.mimeInfo = MDBCreateValueStruct(Imap.directory.handle, NULL);
        if (FetchRequest->messageDetail.mimeInfo) {
            ;
        } else {
            return(STATUS_MEMORY_ERROR);
        }
    } else {
        MDBFreeValues(FetchRequest->messageDetail.mimeInfo);
    }

    if (NMAPSendCommandF(session->store.conn, "MIME %llx\r\n", FetchRequest->message->guid) != -1) {
        ccode = NMAPReadResponse(session->store.conn, session->store.response, sizeof(session->store.response), FALSE);
        while(ccode != 1000) {
            if ((ccode > 2001) && (ccode < 2005)) {
                MDBAddValue(session->store.response, FetchRequest->messageDetail.mimeInfo);
                ccode = NMAPReadResponse(session->store.conn, session->store.response, sizeof(session->store.response), FALSE);
                continue;
            }
            
            return(CheckForNMAPCommError(ccode));
        }

        return(STATUS_CONTINUE);
    }

    return(STATUS_NMAP_COMM_ERROR);
}

__inline static long
SendResponseForFetchFlags(ImapSession *session, FetchStruct *FetchRequest)
{
    long ccode;
    unsigned long i;

    if (ConnWriteF(session->client.conn, "* %lu FETCH (", FetchRequest->messageDetail.sequenceNumber + 1) != -1) {
        if (FetchRequest->flagCount > 0) {
            i = 0;

            do {
                if ((ccode = FetchRequest->flag[i].responder(session, FetchRequest, &(FetchRequest->flag[i]))) == STATUS_CONTINUE) {
                    i++;
                    if (i < FetchRequest->flagCount) {
                        if (ConnWrite(session->client.conn, " ", 1) != -1) {
                            continue;
                        }
                        return(STATUS_ABORT);
                    }
                    break;
                }
                return(ccode);
            } while(TRUE);
        }

        if (ConnWrite(session->client.conn, ")\r\n", 3) != -1) {
            return(STATUS_CONTINUE);
        }
    }
    return(STATUS_ABORT);
}

__inline static long
MessageDetailsGet(ImapSession *session, FetchStruct *FetchRequest)
{
    long ccode;
 
    if (FetchRequest->flags & F_NEED_HEADER) {
        if ((ccode = GetMessageHeader(session, FetchRequest)) == 0) {
            ;
        } else {
            return(ccode);
        }
    }

    if (FetchRequest->flags & F_NEED_MIME) {
        if ((ccode = GetMimeInfo(session, FetchRequest)) == STATUS_CONTINUE) {
            ;
        } else {
            return(ccode);
        }
    }
    return(STATUS_CONTINUE);
}

__inline static long
SetSeenFlag(ImapSession *session, FetchStruct *FetchRequest)
{
    /* Set the flags - got to do this here so the flags include the change - see the rfc */
    if ((FetchRequest->flags & F_BODY_SEEN) && !session->folder.selected.readOnly) {
        return(SetMessageSeenFlag(session, FetchRequest));
    }
    return(STATUS_CONTINUE);
}

__inline static long
SendResponseForMessage(ImapSession *session, FetchStruct *FetchRequest)
{
    long ccode;

    if ((ccode = SetSeenFlag(session, FetchRequest)) == STATUS_CONTINUE) {
        if ((ccode = MessageDetailsGet(session, FetchRequest)) == STATUS_CONTINUE) {
            ccode = SendResponseForFetchFlags(session, FetchRequest);
            MessageDetailsFree(&(FetchRequest->messageDetail));
        }
    }
    return(ccode);
}

__inline static BOOL
MessageStillExists(FetchStruct *FetchRequest)
{
    if (!(FetchRequest->message->flags & STORE_MSG_FLAG_PURGED)) {
        return(TRUE);
    }
    return(FALSE);
}


__inline static long
SendResponseForMessageRange(ImapSession *session, FetchStruct *FetchRequest, unsigned long rangeStart, unsigned long rangeEnd, BOOL *purgedMessage)
{
    long ccode;
    unsigned long currentMessage;

    currentMessage = rangeStart;
    ccode = STATUS_CONTINUE;

    do {
        FetchRequest->message = &(session->folder.selected.message[currentMessage]);
        FetchRequest->messageDetail.sequenceNumber = currentMessage;

        if (MessageStillExists(FetchRequest)) {
            if ((ccode = SendResponseForMessage(session, FetchRequest)) == STATUS_CONTINUE) {
                currentMessage++;
                continue;
            }
            break;
        }

        currentMessage++;
        *purgedMessage = TRUE;
    } while (currentMessage <= rangeEnd);

    return(ccode);
}

int
ImapCommandFetch(void *param)
{
    ImapSession *session = (ImapSession *)param;
    FetchStruct FetchRequest;
    char *nextRange;
    unsigned long rangeStart;
    unsigned long rangeEnd;
    int ccode;
    BOOL purgedMessage = FALSE;

    if ((ccode = CheckState(session, STATE_SELECTED)) == STATUS_CONTINUE) {
        /* rfc 2180 discourages purge notifications during the store command */
        if ((ccode = EventsSend(session, STORE_EVENT_NEW | STORE_EVENT_FLAG)) == STATUS_CONTINUE) {
            memset(&FetchRequest, 0, sizeof(FetchRequest));
      
            FetchRequest.flags = ParseFetchArguments(session, session->command.buffer + 6, &FetchRequest); 
            if (!(FetchRequest.flags & F_ERROR)) {
                nextRange = FetchRequest.messageSet;

                do {
                    ccode = GetMessageRange(session->folder.selected.message, session->folder.selected.messageCount, &(nextRange), &rangeStart, &rangeEnd, FALSE);
                    if (ccode == STATUS_CONTINUE) {
                        if ((ccode = SendResponseForMessageRange(session, &FetchRequest, rangeStart, rangeEnd, &purgedMessage)) == STATUS_CONTINUE) {
                            continue;
                        }
                    }

                    FreeFetchResourcesFailure(&FetchRequest);
                    return(SendError(session->client.conn, session->command.tag, "FETCH", ccode));
                } while (nextRange);

                FreeFetchResourcesSuccess(&FetchRequest);
                if (!purgedMessage) {
                    return(SendOk(session, "FETCH"));
                }
                return(SendError(session->client.conn, session->command.tag, "FETCH", STATUS_REQUESTED_MESSAGE_NO_LONGER_EXISTS));
            }

            if (!(FetchRequest.flags & F_SYSTEM_ERROR)) {
                FreeFetchResourcesFailure(&FetchRequest);    
                return(SendError(session->client.conn, session->command.tag, "FETCH", STATUS_INVALID_ARGUMENT));
            }

            FreeFetchResourcesFailure(&FetchRequest);
            return(SendError(session->client.conn, session->command.tag, "FETCH", STATUS_MEMORY_ERROR));
        }
    }
    return(SendError(session->client.conn, session->command.tag, "FETCH", ccode));
}

int
ImapCommandUidFetch(void *param)
{
    ImapSession *session = (ImapSession *)param;
    FetchStruct FetchRequest;
    int ccode;
    char *nextRange;
    unsigned long rangeStart;
    unsigned long rangeEnd;
    BOOL purgedMessage = FALSE;
    FetchFlagStruct *firstFlag;

    if ((ccode = CheckState(session, STATE_SELECTED)) == STATUS_CONTINUE) {
        /* rfc 2180 discourages purge notifications during the store command */
        if ((ccode = EventsSend(session, STORE_EVENT_NEW | STORE_EVENT_FLAG)) == STATUS_CONTINUE) {
            memmove(session->command.buffer, session->command.buffer + 4, strlen(session->command.buffer + 4) + 1);
            memset(&FetchRequest, 0, sizeof(FetchRequest));
      
            /* Respond with uid even if it has not been explicitly requested */ 
            FetchRequest.flags = F_UID;
            firstFlag = AddNewFlag(&FetchRequest, FetchFlagResponderUid);
            if (firstFlag) {
                FetchRequest.flags = ParseFetchArguments(session, session->command.buffer + 6, &FetchRequest); 
                if (!(FetchRequest.flags & F_ERROR)) {

                    nextRange = FetchRequest.messageSet;

                    do {
                        ccode = GetMessageRange(session->folder.selected.message, session->folder.selected.messageCount, &(nextRange), &rangeStart, &rangeEnd, TRUE);
                        if (ccode == STATUS_CONTINUE) {
                            ccode = SendResponseForMessageRange(session, &FetchRequest, rangeStart, rangeEnd, &purgedMessage);
                            if (ccode == STATUS_CONTINUE) {
                                continue;
                            }
                        }

                        if (ccode == STATUS_UID_NOT_FOUND) {
                            continue;
                        }

                        FreeFetchResourcesFailure(&FetchRequest);
                        return(SendError(session->client.conn, session->command.tag, "UID FETCH", ccode));
                    } while (nextRange);

                    FreeFetchResourcesSuccess(&FetchRequest);
                    if (!purgedMessage) {
                        return(SendOk(session, "UID FETCH"));
                    }
                    return(SendError(session->client.conn, session->command.tag, "UID FETCH", STATUS_REQUESTED_MESSAGE_NO_LONGER_EXISTS));
                }

                if (!(FetchRequest.flags & F_SYSTEM_ERROR)) {
                    FreeFetchResourcesFailure(&FetchRequest);
                    return(SendError(session->client.conn, session->command.tag, "UID FETCH", STATUS_INVALID_ARGUMENT));
                }

                FreeFetchResourcesFailure(&FetchRequest);
            }
            return(SendError(session->client.conn, session->command.tag, "UID FETCH", STATUS_MEMORY_ERROR));
        }
    }
    return(SendError(session->client.conn, session->command.tag, "UID FETCH", ccode));
}

void
CommandFetchCleanup(void)
{
    BongoKeywordIndexFree(FetchFlagsAttIndex);
    BongoKeywordIndexFree(FetchFlagsSoloIndex);
    BongoKeywordIndexFree(FetchFlagsSectionIndex);
}

BOOL
CommandFetchInit(void)
{
    BongoKeywordIndexCreateFromTable(FetchFlagsAttIndex, FetchFlagsAtt, .name, TRUE); 
    if (FetchFlagsAttIndex != NULL) {
        BongoKeywordIndexCreateFromTable(FetchFlagsSoloIndex, FetchFlagsSolo, .name, TRUE); 
        if (FetchFlagsSoloIndex != NULL) {
            BongoKeywordIndexCreateFromTable(FetchFlagsSectionIndex, FetchFlagsSection, .name, TRUE); 
            if (FetchFlagsSectionIndex) {
                return(TRUE);
            }
            BongoKeywordIndexFree(FetchFlagsSoloIndex);
        }
        BongoKeywordIndexFree(FetchFlagsAttIndex);
    } 
    return(FALSE);
}
