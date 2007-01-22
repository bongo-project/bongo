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

static int
EUC_TW_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;

	while (Len<Codec->Len) {
		FlushOutStream(6);

		if (In[0] < 0x80) {
			Out[0]=In[0];
			Out++;
			In++;
			NextCodec->Len++;
			Len++;
		} else {
			CheckInStream(2);

			if (In[0] > 0xa1) {
				In[0]-=0x80;
				In[1]-=0x80;

				if ((In[0]>=0x21 && In[0]<=0x26) || (In[0]==0x42) || (In[0]>=0x44 && In[0]<=0x7d)) {
					if (In[1]>=0x21 && In[1]<0x7f) {
						unsigned int	Index = 94 * (In[0]-0x21) + (In[1]-0x21);
						unsigned short	wc		= 0xfffd;

						if (Index<3102) {
							if (Index<500) {
								wc=cns11643_1_2uni_page21[Index];
							}
						} else if (Index<3290) {
							if (Index<3135) {
								wc=cns11643_1_2uni_page42[Index-3102];
							}
						} else {
							if (Index<8691) {
								wc=cns11643_1_2uni_page44[Index-3290];
							}
						}
						if (wc != 0xfffd) {
							UTF8EncodeChar(wc, Out, Out, NextCodec->Len);
						}
					}
				}
			} else if (In[0]==0x8e) {
				CheckInStream(4);

				if (In[1]>=0xa1 && In[3]>=0xa1) {
					unsigned long	wc=0xfffd;
					unsigned long	Index;

					In[2]-=0x80;
					In[3]-=0x80;
					Index=94*(In[2]-0x21)+(In[3]-0x21);

					switch(In[1]-0xa0) {
						case 1: {
							if ((In[2]>=0x21 && In[2]<=0x26) || (In[2]==0x42) || (In[2]>=0x44 && In[2]<=0x7d)) {
								if (Index< 3102) {
									if (Index<500) {
										wc=cns11643_1_2uni_page21[Index];
									}
								} else if (Index<3290) {
									if (Index<3135) {
										wc=cns11643_1_2uni_page42[Index-3102];
									}
								} else {
									if (Index<8691) {
										wc=cns11643_1_2uni_page44[Index-3290];
									}
								}
							}
							break;
						}

						case 2: {
							if (In[2]>=0x21 && In[2]<=0x72) {
								if (Index<7650) {
									wc=cns11643_2_2uni_page21[Index];
								}
							}
							break;
						}

						case 3: {
							if (In[2]>=0x21 && In[2]<=0x7f) {
								if (Index<6147) {
									wc=cns11643_3_2uni_page21[Index];
								}
							}
							break;
						}
					}

					if (wc != 0xfffd) {
						UTF8EncodeChar(wc, Out, Out, NextCodec->Len);
					}
				}
				In+=2;
				Len+=2;
			}
			In+=2;
			Len+=2;
		}
	}

	EndFlushStream;

	return(Len);
}


static int
EUC_TW_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;
	unsigned long	wc;

	while (Len<Codec->Len) {
		FlushOutStream(6);

		if (*In<0x80) {
			Out[0]=In[0];
			Out++;
			In++;
			NextCodec->Len++;
			Len++;
		} else {
			const Summary16 *summary = NULL;

			CheckUTFInStream;

			UTF8DecodeChar(In, wc, Len, Codec->Len);

			if (wc >= 0x0000 && wc < 0x0100) {
				summary = &cns11643_inv_uni2indx_page00[(wc>>4)];
			} else if (wc >= 0x0200 && wc < 0x03d0) {
				summary = &cns11643_inv_uni2indx_page02[(wc>>4)-0x020];
			} else if (wc >= 0x2000 && wc < 0x22c0) {
				summary = &cns11643_inv_uni2indx_page20[(wc>>4)-0x200];
			} else if (wc >= 0x2400 && wc < 0x2650) {
				summary = &cns11643_inv_uni2indx_page24[(wc>>4)-0x240];
			} else if (wc >= 0x3000 && wc < 0x33e0) {
				summary = &cns11643_inv_uni2indx_page30[(wc>>4)-0x300];
			} else if (wc >= 0x4e00 && wc < 0x9fb0) {
				summary = &cns11643_inv_uni2indx_page4e[(wc>>4)-0x4e0];
			} else if (wc >= 0xfe00 && wc < 0xfff0) {
				summary = &cns11643_inv_uni2indx_pagefe[(wc>>4)-0xfe0];
			}
			if (summary) {
				unsigned short	used	= summary->used;
				unsigned int	i		= wc & 0x0f;

				if (used & ((unsigned short) 1 << i)) {
					unsigned short c;
					unsigned char	plane;

					/* Keep in `used' only the bits 0..i-1. */
					used &= ((unsigned short) 1 << i) - 1;

					/* Add `summary->indx' and the number of bits set in `used'. */
					used = (used & 0x5555) + ((used & 0xaaaa) >> 1);
					used = (used & 0x3333) + ((used & 0xcccc) >> 2);
					used = (used & 0x0f0f) + ((used & 0xf0f0) >> 4);
					used = (used & 0x00ff) + (used >> 8);
					c = cns11643_inv_2charset[summary->indx + used];

					plane=(((c>>8) & 0x80)>>6) | ((c & 0x80)>>7);
					c &= 0x7f7f;

					if (plane==0) {
						Out[0]=(c>>8)+0x80;
						Out[1]=(c & 0xff)+0x80;
						Out+=2;
						NextCodec->Len+=2;
					} else {
						Out[0]=0x8e;
						Out[1]=plane+0xa1;
						Out[2]=(c>>8)+0x80;
						Out[3]=(c & 0xff)+0x80;
						Out+=4;
						NextCodec->Len+=4;
					}
				}
			}
		}
	}

	EndFlushStream;

	return(Len);
}
