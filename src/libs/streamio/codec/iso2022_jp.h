/*
 * Copyright (C) 1999-2001 Free Software Foundation, Inc.
 * This file is part of the GNU LIBICONV Library.
 *
 * The GNU LIBICONV Library is free software; you can redistribute it
 * and/or modify it under the terms of the GNU Library General Public
 * License as published by the Free Software Foundation; either version 2
 * of the License, or (at your option) any later version.
 *
 * The GNU LIBICONV Library is distributed in the hope that it will be
 * useful, but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
 * Library General Public License for more details.
 *
 * You should have received a copy of the GNU Library General Public
 * License along with the GNU LIBICONV Library; see the file COPYING.LIB.
 * If not, write to the Free Software Foundation, Inc., 59 Temple Place -
 * Suite 330, Boston, MA 02111-1307, USA.
 */

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

#define ESC 0x1b

/*
 * The state can be one of the following values.
 */
#define STATE_ASCII          0
#define STATE_JISX0201ROMAN  1
#define STATE_JISX0208       2

static int
ISO2022_JP_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	State=(unsigned char)(Codec->State & 0xff);
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;


	while (Len<Codec->Len) {
		FlushOutStream(6);

		for (;;) {
			if (In[0]==ESC) {
				if ((Codec->Len-Len)<4) {
					if (Codec->EOS) {
						Len=Codec->Len;
					}
					goto Flush;
				}

				if (In[1] == '(') {
					if (In[2] == 'B') {
						State= STATE_ASCII;
						In+=3;
						Len+=3;
						continue;
					} else if (In[2]=='J') {
						State=STATE_JISX0201ROMAN;
						In+=3;
						Len+=3;
						continue;
					} else {
						In+=3;
						Len+=3;
						break;
					}
				} else if (In[1]=='$') {
					if (In[2]=='@' || In[2]=='B') {
						State=STATE_JISX0208;
						In+=3;
						Len+=3;
						continue;
					}
					break;
				} else {
					In++;
					Len++;
					break;
				}
			}
			break;
		}

		if (Len>=Codec->Len) {
			goto Flush;
		}

		switch (State) {
			case STATE_ASCII: {
				*Out=*In;
				Out++;
				In++;
				NextCodec->Len++;
				Len++;
				break;
			}

			case STATE_JISX0201ROMAN: {
				if (In[0]<0x80) {
					if (In[0]==0x5c) {
						*Out=0xa5;
						Out++;
						In++;
						NextCodec->Len++;
						Len++;
					} else if (In[0]==0x7e) {
						UTF8EncodeChar(0x203e, Out, Out, NextCodec->Len);
						In++;
						Len++;
					} else {
						*Out=*In;
						Out++;
						In++;
						NextCodec->Len++;
						Len++;
					}
				} else {
					*Out=*In;
					Out++;
					In++;
					NextCodec->Len++;
					Len++;
				}
				break;
			}

			case STATE_JISX0208: {
				if ((Codec->Len-Len)>=2) {
					if (In[0]<0x80 && In[1]<0x7f) {
						if ((In[0]>=0x21 && In[0]<=0x28) || (In[0]>=0x30 && In[0]<=0x74)) {
							if (In[1]>=0x21) {
								unsigned int	Index=94*(In[0]-0x21)+(In[1]-0x21);
								unsigned long	wc = 0xfffd;

								if (Index<1410) {
									if (Index<690) {
										wc=jisx0208_2uni_page21[Index];
									}
								} else {
									if (Index<7808) {
										wc=jisx0208_2uni_page30[Index-1410];
									}
								}
								if (wc != 0xfffd) {
									UTF8EncodeChar(wc, Out, Out, NextCodec->Len);
								}
							}
						}
					}
					In+=2;
					Len+=2;
					break;
				} else {
					/* Need more data... */
					if (!Codec->EOS) {
						Codec->State=State;
						if (Len >= Codec->Min) {
						    return(Len);
						}

						return(Codec->Len);
					} else {
						/* We needed more data, but there's none to come anymore */
						Len=Codec->Len;
					}
				}
				break;
			}
		}
	}

Flush:
	EndFlushStream;

	Codec->State=State;
        if (Len >= Codec->Min) {
            return(Len);
        }

        return(Codec->Len);
}

#define HIGH_BIT 0x80
#define MAX_UTF8_CHAR_LEN 6

static int
ISO2022_JP_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	State=(unsigned char)(Codec->State & 0xff);
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;
	unsigned long	wc;

	/* Assume ascii first */
	while (Len<Codec->Len) {
		FlushOutStream(6);

		if ((Codec->Len-Len) < MAX_UTF8_CHAR_LEN) {
			char				ControlByte = In[0];
			unsigned long	BytesNeeded	= 0;

			while(ControlByte & HIGH_BIT) {
				BytesNeeded ++;
				ControlByte <<= 1;
			}

			if ((Codec->Len-Len) < BytesNeeded) {
			    if (!Codec->EOS) {
				/* Need more data */
				Codec->State=State;
				if (Len >= Codec->Min) {
				    return(Len);
				}

				return(Codec->Len);
			    }
			    Len=Codec->Len;
			    EndFlushStream;
			    return(Len);
			}
		}

		if (In[0]<0x80) {
			if (State!=STATE_ASCII) {
				Out[0]=ESC;
				Out[1]='(';
				Out[2]='B';
				Out+=3;
				NextCodec->Len+=3;
				State=STATE_ASCII;
			}
			Out[0]=In[0];
			Out++;
			NextCodec->Len++;
			Len++;
			In++;
			continue;
		} else {
			const Summary16 *summary = NULL;

			CheckUTFInStream;

			/* Decode the UTF char */
			UTF8DecodeChar(In, wc, Len, Codec->Len);

			/* This checks JIS 0201 */
			if (wc<0x0080 || (wc>=0xff61 && wc<0xffa0)) {
				unsigned long	count=(State == STATE_JISX0201ROMAN ? 1 : 4);

				FlushOutStream(count);

				if (State!=STATE_JISX0201ROMAN) {
					Out[0]=ESC;
					Out[1]='(';
					Out[2]='J';
					Out+=3;
					NextCodec->Len+=3;
					State=STATE_JISX0201ROMAN;
				}

				if (wc==0x00a5) {
					Out[0]=0x5c;
					Out++;
					NextCodec->Len++;
				} else if (wc==0x203e) {
					Out[0]=0x7e;
					Out++;
					NextCodec->Len++;
				} else if (wc>=0xff61) {
					Out[0]=(unsigned char)(wc-0xfec0);
					Out++;
					NextCodec->Len++;
				} else {
					Out[0]=In[0];
					Out++;
					NextCodec->Len++;
				}
				
			} else {
				/* Wasn't JIS 0201, try 0208 */

				if (wc >= 0x0000 && wc < 0x0100) {
					summary = &jisx0208_uni2indx_page00[(wc>>4)];
				} else if (wc >= 0x0300 && wc < 0x0460) {
					summary = &jisx0208_uni2indx_page03[(wc>>4)-0x030];
				} else if (wc >= 0x2000 && wc < 0x2320) {
					summary = &jisx0208_uni2indx_page20[(wc>>4)-0x200];
				} else if (wc >= 0x2500 && wc < 0x2670) {
					summary = &jisx0208_uni2indx_page25[(wc>>4)-0x250];
				} else if (wc >= 0x3000 && wc < 0x3100) {
					summary = &jisx0208_uni2indx_page30[(wc>>4)-0x300];
				} else if (wc >= 0x4e00 && wc < 0x9fb0) {
					summary = &jisx0208_uni2indx_page4e[(wc>>4)-0x4e0];
				} else if (wc >= 0xff00 && wc < 0xfff0) {
					summary = &jisx0208_uni2indx_pageff[(wc>>4)-0xff0];
				}
				if (summary) {
					unsigned short	used	= summary->used;
					unsigned int	i		= wc & 0x0f;

					if (used & ((unsigned short) 1 << i)) {
						unsigned short c;

						/* Keep in `used' only the bits 0..i-1. */
						used &= ((unsigned short) 1 << i) - 1;

						/* Add `summary->indx' and the number of bits set in `used'. */
						used = (used & 0x5555) + ((used & 0xaaaa) >> 1);
						used = (used & 0x3333) + ((used & 0xcccc) >> 2);
						used = (used & 0x0f0f) + ((used & 0xf0f0) >> 4);
						used = (used & 0x00ff) + (used >> 8);
						c = jisx0208_2charset[summary->indx + used];

						if ((c>>8<0x80) && ((c & 0xff) < 0x80)) {
							unsigned long	count=(State == STATE_JISX0208 ? 2 : 5);

							FlushOutStream(count);

							if (State != STATE_JISX0208) {
								Out[0]=ESC;
								Out[1]='$';
								Out[2]='B';
								Out+=3;
								NextCodec->Len+=3;
								State=STATE_JISX0208;
							}

							Out[0] = (c >> 8); 
							Out[1] = (c & 0xff);
							Out+=2;
							NextCodec->Len+=2;
						}
					}
				}
			}
		}
	}

	if (Codec->EOS) {
		if (State!=STATE_ASCII) {
			FlushOutStream(3);
			Out[0]=ESC;
			Out[1]='(';
			Out[2]='B';
			Out+=3;
			NextCodec->Len+=3;
		}
	}

	EndFlushStream;

	Codec->State=State;

        if (Len >= Codec->Min) {
            return(Len);
        }

        return(Codec->Len);
}


#undef STATE_JISX0208
#undef STATE_JISX0201ROMAN
#undef STATE_ASCII
