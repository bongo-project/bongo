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
#define SO  0x0e
#define SI  0x0f

#define STATE_ASCII							0
#define STATE_TWOBYTE						1
#define STATE2_NONE							0
#define STATE2_DESIGNATED_GB2312			1
#define STATE2_DESIGNATED_CNS11643_1	2
#define STATE3_NONE							0
#define STATE3_DESIGNATED_CNS11643_2	1


static int
ISO2022_CN_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	State1=(unsigned char)(Codec->State & 0xff);
	unsigned char	State2=(unsigned char)((Codec->State >> 8) & 0xff);
	unsigned char	State3=(unsigned char)((Codec->State >> 16) & 0xff);
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;

	while (Len<Codec->Len) {

		FlushOutStream(5);

		for (;;) {
			if (*In==ESC) {
				if ((Codec->Len-Len)<4) {
					if (!Codec->EOS) {
						goto Flush;
					} else {
						Len=Codec->Len;
						goto Flush;
					}
				}

				if (In[1] == '$') {
					if (In[2] == ')') {
						if (In[3] == 'A') {
							State2= STATE2_DESIGNATED_GB2312;
							In+=4;
							Len+=4;
							if (Len>=Codec->Len) {
								goto Flush;
							}
							continue;
						} else if (In[3] == 'G') {
							State2= STATE2_DESIGNATED_CNS11643_1;
							In+=4;
							Len+=4;
							if (Len>=Codec->Len) {
								goto Flush;
							}
							continue;
						} else {
							In+=3;
							Len+=3;
							break;
						}
					} else if (In[2] == '*') {
						if (In[3]=='H') {
							State3=STATE3_DESIGNATED_CNS11643_2;
							In+=4;
							Len+=4;
							if (Len>=Codec->Len) {
								goto Flush;
							}
							continue;
						}
					} else {
						In+=2;
						Len+=2;
						break;
					}
				} else if (In[1]=='N') {
					switch (State3) {
						case STATE3_NONE: {
							In+=2;
							Len+=2;
							if (Len>=Codec->Len) {
								goto Flush;
							}
							continue;
						}

						case STATE3_DESIGNATED_CNS11643_2: {
							if (In[2]<0x80 && In[3]<0x80) {
								unsigned long	wc=0xfffd;

								/* Decode In[2] and In[3] */
								FlushOutStream(2);
								if (In[2]>=0x21 && In[2]<=0x72) {
									if (In[3]>=0x21 && In[3]<=0x7f) {
										unsigned int	Index = 94 * (In[2]-0x21)+(In[3]-0x21);

										if (Index<7650) {
											wc=cns11643_2_2uni_page21[Index];
											if (wc != 0xfffd) {
												UTF8EncodeChar(wc, Out, Out, NextCodec->Len);
											}
										}
									}
								}
								In+=4;
								Len+=4;
								if (Len>=Codec->Len) {
									goto Flush;
								}
								continue;
							}
						}

						default: {
							In+=2;
							Len+=2;
							if (Len>=Codec->Len) {
								goto Flush;
							}
							continue;
						}
					}
				} else {
					In++;
					Len++;
					if (Len>=Codec->Len) {
						goto Flush;
					}
					continue;
				}
			}

			if (*In==SO) {
				if ((State2 != STATE2_DESIGNATED_GB2312) && (State2 != STATE2_DESIGNATED_CNS11643_1)) {
					In++;
					Len++;
				}
				State1 = STATE_TWOBYTE;

				In++;
				Len++;
				if (Len>=Codec->Len) {
					goto Flush;
				}
				continue;
			}

			if (*In==SI) {
				State1=STATE_ASCII;
				In++;
				Len++;
				if (Len>=Codec->Len) {
					goto Flush;
				}
				continue;
			}
			break;
		}

		if (Len>=Codec->Len) {
			goto Flush;
		}

		switch (State1) {
			case STATE_ASCII: {
				*Out=*In;
				if (*Out==0x0a || *Out==0x0d) {
					State2=STATE2_NONE;
					State3=STATE3_NONE;
				}
				Out++;
				In++;
				NextCodec->Len++;
				Len++;
				break;
			}

			case STATE_TWOBYTE: {
				if ((Codec->Len-Len)<2) {
					goto Flush;
				}

				if ((In[0] < 0x80) && (In[1] < 0x80)) {
					switch (State2) {
						case STATE2_NONE: {
							In+=2;
							Len+=2;
							break;
						}

						case STATE2_DESIGNATED_GB2312: {
							if ((Codec->Len-Len) >= 2) {
								if ((In[0] >= 0x21 && In[0] <= 0x29) || (In[0] >= 0x30 && In[0] <= 0x77)) {
									if (In[1] >= 0x21 && In[1] < 0x7f) {
										unsigned int	Index = 94 * (In[0] - 0x21) + (In[1] - 0x21);
										unsigned short	wc		= 0xfffd;

										if (Index < 1410) {
											if (Index < 831) {
												wc = gb2312_2uni_page21[Index];
											}
										} else {
											if (Index < 8178) {
												wc = gb2312_2uni_page30[Index-1410];
											}
										}
										if (wc != 0xfffd) {
											UTF8EncodeChar(wc, Out, Out, NextCodec->Len);
										}
									}
								}
								In	 += 2;
								Len += 2;
								break;
							} else {
								/* Need more data... */
								if (!Codec->EOS) {
									Codec->State = (State3 << 16) | (State2 << 8) | State1;
									if (Len >= Codec->Min) {
									    return(Len);
									}

									return(Codec->Len);
								} else {
									/* We needed more data, but there's none to come anymore */
									Len = Codec->Len;
								}
							}
							break;
						}

						case STATE2_DESIGNATED_CNS11643_1: {
							if ((Codec->Len-Len) >= 2) {
								if ((In[0] >= 0x21 && In[0] <= 0x26) || (In[0] == 0x42) || (In[0] >= 0x44 && In[0] <= 0x7d)) {
									if (In[1]>=0x21 && In[1]<0x7f) {
										unsigned int	Index = 94 * (In[0]-0x21) + (In[1]-0x21);
										unsigned short	wc		= 0xfffd;

										if (Index<3102) {
											if (Index<500) {
												wc=cns11643_1_2uni_page21[Index];
											}
										} else if (Index<3290) {
											if (Index<3135) {
												wc=cns11643_1_2uni_page42[Index];
											}
										} else {
											if (Index<8691) {
												wc=cns11643_1_2uni_page44[Index];
											}
										}
										if (wc != 0xfffd) {
											UTF8EncodeChar(wc, Out, Out, NextCodec->Len);
										}
									}
								}
								In += 2;
								Len += 2;
								break;
							} else {
								/* Need more data... */
								if (!Codec->EOS) {
									Codec->State = (State3 << 16) | (State2 << 8) | State1;
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

						default: {
							In+=2;
							Len+=2;
							break;
						}
					}
				} else {
					In+=2;
					Len+=2;
				}
				break;
			}
		}
	}

Flush:
	EndFlushStream;

	Codec->State=(State3<<16) | (State2<<8) | State1;
        if (Len >= Codec->Min) {
            return(Len);
        }

        return(Codec->Len);
}


static int
ISO2022_CN_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	State1=(unsigned char)(Codec->State & 0xff);
	unsigned char	State2=(unsigned char)((Codec->State >> 8) & 0xff);
	unsigned char	State3=(unsigned char)((Codec->State >> 16) & 0xff);
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;
	unsigned long	wc;

	/* Assume ascii first */
	while (Len<Codec->Len) {
		FlushOutStream(6);

		if (In[0]<0x80) {
			if ((Codec->Len-Len)<(unsigned long)(State1==STATE_ASCII ? 1: 2)) {
				/* Need more data */
				if (!Codec->EOS) {
					Codec->State=(State3<<16) | (State2<<8) | State1;
					if (Len >= Codec->Min) {
					    return(Len);
					}

					return(Codec->Len);
				} else {
					Len=Codec->Len;
					EndFlushStream;
					return(Len);
				}
			}

			if (State1!=STATE_ASCII) {
				*Out=SI;
				Out++;
				NextCodec->Len++;
				State1=STATE_ASCII;
			}
			if (In[0]==0x0a || In[0]==0x0d) {
				State2=STATE2_NONE;
				State3=STATE3_NONE;
			}
			*Out=In[0];
			Out++;
			NextCodec->Len++;
			Len++;
			In++;
			continue;
		} else {
			/* We've got a UTF-8 char */

			/*
				We need to figure out which encoding we need to use;
				it's either gb2312, cns11643 plane 1 or cns11643 plane 2
			*/
			if ((Codec->Len-Len)>=4) {
				const Summary16 *summary = NULL;

				/* Decode the UTF char */
				CheckUTFInStream;

				UTF8DecodeChar(In, wc, Len, Codec->Len);

				/* Are we GB2312? */
				if (wc >= 0x0000 && wc < 0x0460) {
					summary = &gb2312_uni2indx_page00[(wc>>4)];
				} else if (wc >= 0x2000 && wc < 0x2650) {
					summary = &gb2312_uni2indx_page20[(wc>>4)-0x200];
				} else if (wc >= 0x3000 && wc < 0x3230) {
					summary = &gb2312_uni2indx_page30[(wc>>4)-0x300];
				} else if (wc >= 0x4e00 && wc < 0x9cf0) {
					summary = &gb2312_uni2indx_page4e[(wc>>4)-0x4e0];
				} else if (wc >= 0x9e00 && wc < 0x9fb0) {
					summary = &gb2312_uni2indx_page9e[(wc>>4)-0x9e0];
				} else if (wc >= 0xff00 && wc < 0xfff0) {
					summary = &gb2312_uni2indx_pageff[(wc>>4)-0xff0];
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
						c = gb2312_2charset[summary->indx + used];

						if ((c>>8<0x80) && ((c & 0xff) < 0x80)) {
							unsigned long	count=(State2 == STATE2_DESIGNATED_GB2312 ? 0 : 4) + (State1 == STATE_TWOBYTE ? 0 : 1) + 2;

							FlushOutStream(count);

							if (State2 != STATE2_DESIGNATED_GB2312) {
								Out[0]=ESC;
								Out[1]='$';
								Out[2]=')';
								Out[3]='A';
								Out+=4;
								NextCodec->Len+=4;
								State2=STATE2_DESIGNATED_GB2312;
							}
							if (State1 != STATE_TWOBYTE) {
								Out[0]=SO;
								Out++;
								NextCodec->Len++;
								State1 = STATE_TWOBYTE;
							}

							Out[0] = (c >> 8); 
							Out[1] = (c & 0xff);
							Out+=2;
							NextCodec->Len+=2;
						}
						/* We found the char, skip the other checks */
						continue;
					}
				}

				/* We're not GB2312; check CNS11643 */
				FlushOutStream(4);
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

						/* Is it plane 1? */
						if (plane==0 && (c>>8<0x80) && ((c & 0xff) < 0x80)) {
							unsigned long	count=(State2 == STATE2_DESIGNATED_CNS11643_1 ? 0 : 4) + (State1 == STATE_TWOBYTE ? 0 : 1) + 2;

							FlushOutStream(count);

							if (State2 != STATE2_DESIGNATED_CNS11643_1) {
								Out[0]=ESC;
								Out[1]='$';
								Out[2]=')';
								Out[3]='G';
								Out+=4;
								NextCodec->Len+=4;
								State2=STATE2_DESIGNATED_CNS11643_1;
							}
							if (State1 != STATE_TWOBYTE) {
								Out[0]=SO;
								Out++;
								NextCodec->Len++;
								State1 = STATE_TWOBYTE;
							}

							Out[0] = (c >> 8); 
							Out[1] = (c & 0xff);
							Out+=2;
							NextCodec->Len+=2;
						} else if (plane==1 && (c>>8<0x80) && ((c & 0xff) < 0x80)) {
							unsigned long	count=(State3 == STATE3_DESIGNATED_CNS11643_2 ? 0 : 4) + 4;

							FlushOutStream(count);

							if (State3 != STATE3_DESIGNATED_CNS11643_2) {
								Out[0]=ESC;
								Out[1]='$';
								Out[2]='*';
								Out[3]='H';
								Out+=4;
								NextCodec->Len+=4;
								State3=STATE3_DESIGNATED_CNS11643_2;
							}
							Out[0]=ESC;
							Out[1]='N';
							Out[2]=(c>>8);
							Out[3]=(c & 0xff);
							Out+=4;
							NextCodec->Len+=4;
						} else {
							/* If we get here we've gotten an illegal sequence, but we've already skipped the input (via UTF8DecodeChar), so move on */
						}
						continue;
					}
				}
				/* If we get here we've gotten an illegal sequence, but we've already skipped the input (via UTF8DecodeChar), so move on */
			} else {
				if (!Codec->EOS) {
					/* Need more data */
					Codec->State=(State3<<16) | (State2<<8) | State1;
					if (Len >= Codec->Min) {
					    return(Len);
					}

					return(Codec->Len);
				} else {
					/* Crap */
					Len=Codec->Len;
					continue;
				}
			}
		}
	}

	if (Codec->EOS) {
		if (State1!=STATE_ASCII) {
			*Out=SI;
			Out++;
			NextCodec->Len++;
		}
	}

	EndFlushStream;

	Codec->State=(State3<<16) | (State2<<8) | State1;

        if (Len >= Codec->Min) {
            return(Len);
        }

        return(Codec->Len);
}

#undef STATE3_DESIGNATED_CNS11643_2
#undef STATE3_NONE
#undef STATE2_DESIGNATED_CNS11643_1
#undef STATE2_DESIGNATED_GB2312
#undef STATE2_NONE
#undef STATE_TWOBYTE
#undef STATE_ASCII
