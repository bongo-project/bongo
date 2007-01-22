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

#define	LINK_MAILTO	1
#define	LINK_HTTP	2
#define	LINK_URL		3
#define	LINK_IMG		4


#define	TYPE_SKIP_ON		1
#define	TYPE_SKIP_OFF		2
#define	TYPE_ADD_URL		3
#define	TYPE_ADD_IMG		4
#define	TYPE_CHARSET		5
#define	TYPE_IGNORE			6

#ifndef HTML_TAG_STRUCT
#define HTML_TAG_STRUCT
typedef struct {
	unsigned char	*Tag;
	unsigned long	Type;
	unsigned long	TagLength;
	unsigned long	EndTagID;
	unsigned char	*Marker;
	unsigned long	MarkerLength;
} HTMLTagStruct;
#endif

/* 
	WARNING

	When editing the list below, make sure that the marker is all 
	uppercase and that you update NumOfTags with the correct 
	number of tags, excluding the terminating NULL tag

*/

/* This section is a copy of HTML_Decode.  I don't know if it works.  But it is not used. */ 

#if 0

#define	NumOfTags	8

HTMLTagStruct TagList[] = {
	"<HEAD",		TYPE_SKIP_ON,			5,		1,		NULL,				0,
	"</HEAD>",	TYPE_SKIP_OFF,			7,		1,		NULL,				0,
	"<META ",	TYPE_CHARSET,			6,		0,		"CHARSET=",		8,
//	"<IMG",		TYPE_ADD_IMG,			4,		0,		"SRC=",			4,
	"<A",			TYPE_ADD_URL,			2,		0,		"HREF=",			5,
	"<BODY",		TYPE_IGNORE,			5,		0,		NULL,				0,
	"</BODY>",	TYPE_IGNORE,			7,		0,		NULL,				0,
	"<HTML",		TYPE_IGNORE,			5,		0,		NULL,				0,
	"</HTML>",	TYPE_IGNORE,			7,		0,		NULL,				0,
	NULL,			0,							0,		0,		NULL,				0
};

/* 
	This takes an HTML message and makes it viewable 

	URL should be set to the url of the server to use for the redirects
	Charset should be set to the charset of the user before starting the stream
*/

#define	MINIMUM_HTML_PROCESS_SIZE	50




int
WML_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned char	*End=In+Codec->Len;
	unsigned long	Len = 0;
	unsigned char	*BlockStart;
	unsigned char	*BlockEnd;
	unsigned long	i;
	unsigned long	j;
	unsigned long	k;

	while (Len<Codec->Len) {
		FlushOutStream(6);

		if (In[0]!='<') {
			if (Codec->State==0) {
				Out[0]=In[0];
				Out++;
				In++;
				Len++;
				NextCodec->Len++;
				continue;
			}
			/* We're skipping the chars */
			In++;
			Len++;
			continue;
		} else {
			/* Look for closing tag */
			BlockStart=In;
			BlockEnd=In+1;
			i=0;	/* i = InQuote */
			while (BlockEnd<End) {
				if (!i && *BlockEnd=='>') {
					break;
				} else if (*BlockEnd=='"') {
					i=~i;
				}
				BlockEnd++;
			}
			if (BlockEnd==End) {
				/* Need more data */
				if (!Codec->EOS && ((Len>MINIMUM_HTML_PROCESS_SIZE) || ((unsigned long)(Codec->End-Codec->Start-MINIMUM_HTML_PROCESS_SIZE)>Codec->Len))) {
				    if (Len >= Codec->Min) {
					return(Len);
				    }

				    return(Codec->Len);
				}

				/* 
					If we get here, we didn't have stuff to flush 
					but our buffer is too small to have then end 
					bracket; we'll just pass the data through
				*/
				
				if (Codec->State==0) {
					Out[0]=In[0];
					Out++;
					In++;
					Len++;
					NextCodec->Len++;
					continue;
				}
				/* We're skipping the chars */
				In++;
				Len++;
				continue;
			}

			/* Make a decision about the tag */
			k=BlockEnd-BlockStart+1;
			for (i=0; i<NumOfTags; i++) {
				if ((TagList[i].TagLength<=k) && QuickNCmp(TagList[i].Tag, BlockStart, TagList[i].TagLength)) {
					/* We have found a matching tag */

					switch (TagList[i].Type) {
						case TYPE_IGNORE: {
							Len+=BlockEnd-BlockStart+1;
							In+=BlockEnd-BlockStart+1;
							goto HandledTag;
						}

						case TYPE_SKIP_ON: {
							if (Codec->State==0) {
								Codec->State=1 | (TagList[i].EndTagID<<16);
							}
							Len+=BlockEnd-BlockStart+1;
							In+=BlockEnd-BlockStart+1;
							goto HandledTag;
						}

						case TYPE_SKIP_OFF: {
							if (Codec->State!=0) {
								if (((Codec->State>>16) & 0xffff)==i) {
									Codec->State=0;
								}
							}
							Len+=BlockEnd-BlockStart+1;
							In+=BlockEnd-BlockStart+1;
							goto HandledTag;
						}

						case TYPE_CHARSET: {
							unsigned char	*ptr;
							unsigned char	*endptr;
							unsigned char	*cmpptr;

							ptr=BlockStart+TagList[i].TagLength;
							endptr=BlockEnd-TagList[i].MarkerLength;
							cmpptr=TagList[i].Marker;
							j=TagList[i].MarkerLength;

							while (ptr<endptr) {
								if (toupper(*ptr) == *cmpptr) {
									if (QuickNCmp(ptr, cmpptr, j)) {
										ptr+=j;
										while (isspace(*ptr) || *ptr=='"') {
											ptr++;
										}
										endptr=ptr;
										while (!isspace(*endptr) && *endptr!='"' && *endptr!='>') {
											endptr++;
										}
										*endptr='\0';

										if (Codec->Charset) {
											StreamCodecFunc	Func;

											Func=FindCodec(ptr, FALSE);
											if (Func) {
												((StreamStruct *)(Codec->Charset))->Codec=Func;
											}
										}

										Len+=BlockEnd-BlockStart+1;
										In+=BlockEnd-BlockStart+1;
										goto HandledTag;
									}
								}
								ptr++;
							}
							i=NumOfTags;
							continue;
						}

						case TYPE_ADD_URL:
						case TYPE_ADD_IMG: {
							unsigned char	*ptr;
							unsigned char	*endptr;
							unsigned char	*cmpptr;


							if (Codec->State!=0) {
								Len+=BlockEnd-BlockStart+1;
								In+=BlockEnd-BlockStart+1;
								goto HandledTag;
							}

							ptr=BlockStart+TagList[i].TagLength;
							endptr=BlockEnd-TagList[i].MarkerLength;
							cmpptr=TagList[i].Marker;
							j=TagList[i].MarkerLength;

							while (ptr<endptr) {
								if (toupper(*ptr) == *cmpptr) {
									if (QuickNCmp(ptr, cmpptr, j)) {
										ptr+=j;
										while (isspace(*ptr) || *ptr=='"') {
											ptr++;
										}
										endptr=ptr;
										while (!isspace(*endptr) && *endptr!='"' && *endptr!='>') {
											endptr++;
										}
										//*endptr='\0';

										/* Regular URL or mailto: URL */
										if (QuickNCmp(ptr, "mailto:", 7)==FALSE) {
											/* Make room in the next codec */
											j=ptr-BlockStart;
											if (!Codec->URL) {
												XplConsolePrintf("\rHTML.H: Missing required URL argument\n");
												Codec->URL="";
											}
											k=strlen(Codec->URL);
											FlushOutStream(j+k);

											/* Send up to the prefix */
											memcpy(Out, BlockStart, j);
											Out+=j;
											NextCodec->Len+=j;

											/* Send the prefix */
											memcpy(Out, Codec->URL, k);
											Out+=k;
											NextCodec->Len+=k;

											/* Now prepare the input stream */
											Len+=j;
											In+=j;
											goto HandledTag;
										} else {
											/* It's a mailto: URL, strip the mailto and point to us instead */
											unsigned char	*URLptr;

											/* Make room in the next codec */
											j=ptr-BlockStart;
											if (!Codec->URL) {
												XplConsolePrintf("\rHTML.H: Missing required URL argument\n");
												Codec->URL="";
											}

											URLptr=(unsigned char *)Codec->URL+strlen(Codec->URL)+1;
											k=strlen(URLptr);
											FlushOutStream(j+k);

											/* Send up to the prefix */
											memcpy(Out, BlockStart, j);
											Out+=j;
											NextCodec->Len+=j;

											/* Send "our" mailto: prefix */
											memcpy(Out, URLptr, k);
											Out+=k;
											NextCodec->Len+=k;

											/* Now prepare the input stream */
											Len+=j+7;	/* The 7 is for the mailto: tag */
											In+=j+7;
											goto HandledTag;
										}
									}
								}
								ptr++;
							}
							i=NumOfTags;
							continue;
						}
					}
				}
			}

//			Len+=BlockEnd-BlockStart+1;
//			In+=BlockEnd-BlockStart+1;

			if (Codec->State==0) {
				Out[0]=In[0];
				Out++;
				In++;
				Len++;
				NextCodec->Len++;
				continue;
			} else {
				/* We're skipping the chars */
				In++;
				Len++;
				continue;
			}
HandledTag: ;
		}
	}

	if ((NextCodec->Len > 0) || (Codec->EOS)) {
	    unsigned long bytesProcessed;

	    if (Codec->EOS) {
		if (Len == Codec->Len) {
		    NextCodec->EOS = TRUE;
		}
		Codec->State = 0;
	    }
	    
	    bytesProcessed = NextCodec->Codec(NextCodec, NextCodec->Next);
	    if (bytesProcessed >= NextCodec->Len) {
		NextCodec->Len = 0;
	    } else {
		NextCodec->Len -= bytesProcessed;
		memmove(NextCodec->Start, Out - NextCodec->Len, NextCodec->Len);
	    }
	}

        if (Len >= Codec->Min) {
            return(Len);
        }

        return(Codec->Len);
}

#endif


/*
	This takes a text message and makes it viewable	in wml
*/

/* 
	URL should be set to the url of the server to use for the redirects
	Charset should be set to the charset of the user before starting the stream
*/

static int
WML_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;

	while (Len<Codec->Len) {
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
				memcpy(Out, "<br/>", 5);
				In++;
				Len++;
				Out+=5;
				NextCodec->Len+=5;
				continue;
			}

			case '$': {
				memcpy(Out, "$$", 2);
				In++;
				Len++;
				Out+=2;
				NextCodec->Len+=2;
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
