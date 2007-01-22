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

#define RFC2822_LONG_LINE									50

/* This codec expects the input stream to be an RFC822 header */
/* The output will be the same header with out folding */
static int
RFC822_Fold_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
    unsigned char	*In = Codec->Start;
    unsigned char       *InEnd = Codec->Start + Codec->Len;
    unsigned char	*Out = NextCodec->Start + NextCodec->Len;
    unsigned char	*OutEnd = NextCodec->End;
    unsigned long	Len = 0;
    unsigned char       *ptr;
    unsigned long       lineLen;
    unsigned long       skipLen;

    while (Len < Codec->Len) {
        ptr = In;
        for (;;) {
            if ((ptr + 2) < InEnd) {
                if (*ptr == '\r') {
                    if (*(ptr + 1) == '\n') {
                        if ((*(ptr + 2) == ' ') || (*(ptr + 2) == '\t')) {
                            skipLen = 2;
                            break;
                        }
                        ptr++;
                    }
                    ptr++;
                    continue;
                }

                if (*ptr == '\n') {
                    if ((*(ptr + 1) == ' ') || (*(ptr + 1) == '\t')) {
                        skipLen = 1;
                        break;
                    }
                }

                ptr++;
                continue;
            }

            /* boundary condition */
            skipLen = 0;
            break;
        }

        lineLen = ptr - In;
        FlushOutStream(lineLen);
        memcpy(Out, In, lineLen);
        Out += lineLen;
        In += lineLen;
        NextCodec->Len += lineLen;
        Len += lineLen;
                        
        if (skipLen) {
            In += skipLen;
            Len += skipLen;
            continue;
        }
        
        /* we need more data */
        if (!Codec->EOS) {
            if (Len) {
                Codec->StreamData2 = NULL;
                EndFlushStream;
                return(Len);
            }

            if (!Codec->StreamData2) {
                /* we did not return 0 last time so we can this time */
                Codec->StreamData2 = In;
                EndFlushStream;
                return(0);
            }
        }

        lineLen = InEnd - In;
        FlushOutStream(lineLen);
        memcpy(Out, In, lineLen);
        Out += lineLen;
        In += lineLen;
        NextCodec->Len += lineLen;
        Len += lineLen;
    }

    EndFlushStream;

    Codec->StreamData2 = NULL;
    return(Len);
}

#define RFC822_HEADER_NAME 0
#define RFC822_HEADER_VALUE_SEND 1
#define RFC822_HEADER_VALUE_SKIP 2

/* This codec expects the input stream to be a flattened RFC822 header */
/* If a value is found in StreamData, only the value of that header will */
/* be passed on. Otherwise the values of all headers are passed on. */
/* In all cases the header names are removed. */
/* The rfc1522 (rfc2047) decoder is a logical next codec. */

static int
RFC822_Header_Value_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
    unsigned char	*In = Codec->Start;
    unsigned char       *InEnd = Codec->Start + Codec->Len;
    unsigned char	*Out = NextCodec->Start+NextCodec->Len;
    unsigned char	*OutEnd = NextCodec->End;
    unsigned long	Len = 0;

    unsigned char       *ptr;

    while (Len < Codec->Len) {
        if (Codec->State == RFC822_HEADER_NAME) {
            unsigned long nameLen;

            ptr = In;
            for(;;) {
                if (ptr < InEnd) {
                    if (*ptr != ':') {
                        ptr++;
                        continue;
                    }

                    if (Codec->StreamData) {
                        *ptr = '\0';
                        if (XplStrCaseCmp(Codec->StreamData, In) == 0) {
                            Codec->State = RFC822_HEADER_VALUE_SEND;
                        } else {
                            Codec->State = RFC822_HEADER_VALUE_SKIP;
                        }
                        *ptr = ':';
                    } else {
                        Codec->State = RFC822_HEADER_VALUE_SEND;
                    }

                    ptr++;
                    nameLen = ptr - In;
    
                    In += nameLen;
                    Len += nameLen;
                    break;
                }

                /* we haven't seen a ':' yet, and we are out of data */
                if (!Codec->EOS) {
                    if (Len) {
                        Codec->StreamData2 = NULL;
                        EndFlushStream;
                        return(Len);
                    }

                    if (!Codec->StreamData2) {
                        /* we did not return 0 last time so we can this time */
                        Codec->StreamData2 = In;
                        EndFlushStream;
                        return(0);
                    }
                }

                nameLen = InEnd - In;
                In += nameLen;
                Len += nameLen;
                break;
            }
            continue;
        }

        if (Codec->State == RFC822_HEADER_VALUE_SEND) {
            unsigned long valueLen;

            ptr = In;
            while (ptr < InEnd) {
                if (*ptr != '\n') {
                    ptr++;
                    continue;
                }

                Codec->State = RFC822_HEADER_NAME;
                ptr++;
                break;
            }

            valueLen = ptr - In; 
            FlushOutStream(valueLen);
            memcpy(Out, In, valueLen);
            Out += valueLen;
            In += valueLen;
            NextCodec->Len += valueLen;
            Len += valueLen;
            continue;
        }

        if (Codec->State == RFC822_HEADER_VALUE_SKIP) {
            while (Len < Codec->Len) {
                if (*In != '\n') {
                    In++;
                    Len++;
                    continue;
                }

                In++;
                Len++;
                Codec->State = RFC822_HEADER_NAME;
                break;
            }
            continue;
        }
    }

    Codec->StreamData2 = NULL;
    EndFlushStream;
    return(Len);
}
