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

__inline static BOOL
WordContains(unsigned char *word, unsigned long wordLen, unsigned char *substring, unsigned long substringLen)
{
    unsigned long i;

    if (wordLen < substringLen) {
        return(FALSE);
    }

    for (i = 0; i < (wordLen - substringLen + 1); i++) {
        if (toupper(word[i]) != toupper(*substring)) {
            continue;
        }

        if (XplStrNCaseCmp(word + i, substring, substringLen) != 0) {
            continue;
        }
        return(TRUE);
    }

    return(FALSE);
}

/* This codec will search through an utf-8 input stream for words that contain */
/* the substring specified in StreamData. The code that links this codec into a stream */
/* is responsible for setting StreamData to point to the substring being searched for.  */
/* The output will be a list of words containing the substring delimited by a CRLF */
static int
Search_Subword_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
    unsigned char *In = Codec->Start;
    unsigned char *Out = NextCodec->Start + NextCodec->Len;
    unsigned char *OutEnd = NextCodec->End;
    unsigned long Len = 0;
    unsigned long substringLen;
    unsigned char *substring;
    unsigned char *word = NULL;
    unsigned long wordLen;
    unsigned long remainingLen;
        
    substring = Codec->StreamData;
    substringLen = strlen(substring);

    if (substringLen > 0) {
        for(;;) {
            if (Len < Codec->Len) {
                if (isspace(*In)) {
                    word = NULL;
                    In++;
                    Len++;
                    continue;
                }

                wordLen = 0;
                remainingLen = Codec->Len - Len;
                do {
                    if (isspace(In[wordLen])) {
                        break;
                    }
                    wordLen++;
                } while (wordLen < remainingLen);

                if ((wordLen == remainingLen) && (!Codec->EOS) && (Len > 0)) {
                    return(Len);
                }

                /* wordContains(word, wordLen, substring, substringLen); */
                if (WordContains(In, wordLen, substring, substringLen)) {
                    /* Pass the word through */
                    FlushOutStream(wordLen + 2);
                    memcpy(Out, In, wordLen);
                    Out += wordLen;
                    memcpy(Out, "\r\n", 2);
                    Out += 2;
                    NextCodec->Len += (wordLen + 2);
                }

                In += wordLen;
                Len += wordLen;
                continue;
            }
            break;
        }
    }
    EndFlushStream;
    
    return(Len);
}

__inline static unsigned char *
FindWordBreakAfter(unsigned char *stringStart, unsigned char *stringEnd)
{
    unsigned char *ptr = stringStart;

    while(ptr < stringEnd) {
        if (!isspace(*ptr)) {
            ptr++;
            continue;
        }
        return(ptr);
    }
    return(NULL);
}

__inline static unsigned char *
FindWordBreakBefore(unsigned char *stringEnd, unsigned char *stringStart)
{
    unsigned char *ptr = stringEnd - 1;

    while(ptr > stringStart) {
        if (!isspace(*ptr)) {
            ptr--;
            continue;
        }
        ptr++;
        return(ptr);
    }
    return(NULL);
}

/* This codec will search through an utf-8 input stream for phrases that contain */
/* the substring specified in StreamData. The code that links this codec into a stream */
/* is responsible for setting StreamData to point to the substring being searched for.  */
/* The output will be a list of phrases containing the substring delimited by a CRLF */
static int
Search_Substring_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
    unsigned char *In = Codec->Start;
    unsigned char *InEnd = Codec->Start + Codec->Len;
    unsigned char *Out = NextCodec->Start + NextCodec->Len;
    unsigned char *OutEnd = NextCodec->End;
    unsigned long Len = 0;
    unsigned long substringLen;
    unsigned char *substring;
    unsigned long phraseLen;
    unsigned long remainingLen;
    unsigned char *cur;
    unsigned char *phraseStart;
    unsigned char *phraseEnd;
    unsigned long count;
        
    substring = Codec->StreamData;
    substringLen = strlen(substring);

    if (substringLen > 0) {
        for(;;) {
            cur = In;
            while (cur < InEnd) {
                if (toupper(*cur) != toupper(*substring)) {
                    cur++;
                    continue;
                }
                
                /* first character found */
                remainingLen = InEnd - cur;

                if (substringLen <= remainingLen) {
                    /* we have enough data in the buffer to verify a match */
                    if (XplStrNCaseCmp(cur, substring, substringLen) != 0) {
                        /* not a match, keep going! */
                        cur++;
                        continue;
                    }                
                
                    /* we have a match!! */

                    /* find where the match phrase begins and ends */
                    if (!isspace(substring[substringLen - 1])) {
                        phraseEnd = FindWordBreakAfter(cur + substringLen, InEnd);
                        if (!phraseEnd) {
                            if (!Codec->EOS) {
                                /* get more data */
                                break;
                            }
                            phraseEnd = InEnd;
                        }                        
                    } else {
                        phraseEnd = cur + substringLen;
                    }

                    if (!isspace(substring[0])) {
                        phraseStart = FindWordBreakBefore(cur, In);
                        if (!phraseStart) {
                            phraseStart = In;
                        }
                    } else {
                        phraseStart = cur;
                    }


                    /* Pass the phrase containing the substring through */
                    phraseLen = phraseEnd - phraseStart;
                    FlushOutStream(phraseLen + 2);
                    memcpy(Out, phraseStart, phraseLen);
                    Out += phraseLen;
                    memcpy(Out, "\r\n", 2);
                    Out += 2;
                    NextCodec->Len += (phraseLen + 2);

                    /* process everything up to and including the first char in the substring */
                    /* we want to find additional instances of the substring in the same word without */
                    /* finding the same one */
                    cur++;
                    Len += (cur - In);
                    In = cur;
                    continue;
                }

                if (XplStrNCaseCmp(cur, substring, remainingLen) != 0) {
                    cur++;
                    continue;
                }
                /* what we have in the buffer does match */
                /* but we need more info */
                break;
            }

            /* more data is needed */
            /* possible reasons: */
            /* 1. the first character of the substring is not in the buffer */
            /* 2. we need more data to verify a match (see break above) */
            /* 3. we have a match, but we do not have the whole phrase (see break above) */
            
            if (Codec->EOS) {
                /* there is no more data to be had */
                Len = Codec->Len;
                In = InEnd;
                EndFlushStream;
                Codec->StreamData2 = NULL;
                return(Codec->Len);
            }

            /* We are going to ask for more data, but we want to make as much room as possible in the buffer to receive it. */
            /* Anything before the current phrase can be be dropped, so we want to */ 
            /* look for the beginning of the phrase of which 'cur' is a member */            
            if ((cur < InEnd) && (isspace(*cur))) {
                count = cur - In;
                In += count;
                Len += count;                
            } else {
                phraseStart = FindWordBreakBefore(cur, In);
                if (phraseStart) {
                    count = phraseStart - In;
                    In += count;
                    Len += count;;
                }
            }

            if (Len) {
                /* we do have data that can be dropped */
                Codec->StreamData2 = NULL;
                EndFlushStream;
                return(Len);
            }

            if (!Codec->StreamData2) {
                /* we have not returned zero before */
                Codec->StreamData2 = (void *)Codec;
                /* it really does not matter what value is in StreamData2 as long as it in non-null */
                /* we are using it to determine if we have returned 0 before */
                return(0);
            }
            
            /* we have already returned zero once and we did not get enough data to find the substring */
            
            if ((cur != InEnd) && (cur != In)) {
                /* flush the part of the word that preceeds the match */
                /* maybe we can get more room */
                Len += (cur - In);
                In = cur;
                return(Len);
            }

            /* there might be a substring in a long continuous word, but this codec doesn't have room to fit the whole word in the buffer */
            Len = Codec->Len;
            In = InEnd;
            return(Codec->Len);
        }
    }
    EndFlushStream;
    
    return(Len);
}
