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

#define STATE_ASCII						0
#define STATE_TWOBYTE					1
#define STATE2_NONE						0
#define STATE2_DESIGNATED_KSC5601	1

static int
ISO2022_KR_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	State1=(unsigned char)(Codec->State & 0xff);
	unsigned char	State2=(unsigned char)((Codec->State >> 8) & 0xff);
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;

	while (Len<Codec->Len) {
		FlushOutStream(6);

		for (;;) {
			if (*In==ESC) {
				CheckInStream(4);

				if (In[1] == '$') {
					if (In[2] == ')') {
						if (In[3] == 'C') {
							State2= STATE2_DESIGNATED_KSC5601;
							In+=4;
							Len+=4;
							if ((Codec->Len-Len)<1) {
								goto Flush;
							}
							continue;
						} else {
							In+=3;
							Len+=3;
							break;
						}
					} else {
						In+=2;
						Len+=2;
						break;
					}
				} else {
					In++;
					Len++;
					break;
				}
			}

			if (*In==SO) {
				if (State2 != STATE2_DESIGNATED_KSC5601) {
					In++;
					Len++;
				}
				State1 = STATE_TWOBYTE;

				In++;
				Len++;
				if ((Codec->Len-Len)<1) {
					goto Flush;
				}
				continue;
			}

			if (*In==SI) {
				State1=STATE_ASCII;
				In++;
				Len++;
				if ((Codec->Len-Len)<1) {
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

				if (State2!=STATE2_DESIGNATED_KSC5601) {
					In++;
					Len++;
					break;
				}

				if ((In[0] < 0x80) && (In[1] < 0x80)) {
					if ((In[0] >= 0x21 && In[0] <= 0x2c) || (In[0] >= 0x30 && In[0] <= 0x48) || (In[0] >= 0x4a && In[0] <= 0x7d)) {
						if ((Codec->Len-Len) >= 2) {
							if (In[1] >= 0x21 && In[1] < 0x7f) {
								unsigned int		i = 94 * (In[0] - 0x21) + (In[1] - 0x21);
								unsigned short		wc = 0xfffd;

								if (i < 1410) {
									if (i < 1115) {
										wc = KSC5601_UTF8_CP21[i];
									}
								} else if (i < 3854) {
									if (i < 3760) {
										wc = KSC5601_UTF8_CP30[i-1410];
									}
								} else {
									if (i < 8742) {
										wc = KSC5601_UTF8_CP4a[i-3854];
									}
								}
								if (wc != 0xfffd) {
									UTF8EncodeChar(wc, Out, Out, NextCodec->Len);
								}
							}
							In+=2;
							Len+=2;
							break;
						} else {
							/* Need more data... */
							if (!Codec->EOS) {
								Codec->State=(State2<<8) | State1;
								if (Len >= Codec->Min) {
								    return(Len);
								}

								return(Codec->Len);
							} else {
								/* We needed more data, but there's none to come anymore */
								Len=Codec->Len;
							}
						}
					} else {
						In+=2;
						Len+=2;
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

	Codec->State=(State2<<8) | State1;
        if (Len >= Codec->Min) {
            return(Len);
        }

        return(Codec->Len);
}

static int
ISO2022_KR_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	State1=(unsigned char)(Codec->State & 0xff);
	unsigned char	State2=(unsigned char)((Codec->State >> 8) & 0xff);
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
			    Codec->State=(State2<<8) | State1;
			    if (Len >= Codec->Min) {
				return(Len);
			    }

			    return(Codec->Len);
			}
			Len=Codec->Len;
			EndFlushStream;
			return(Len);
		    }

			if (State1!=STATE_ASCII) {
				*Out=SI;
				Out++;
				NextCodec->Len++;
				State1=STATE_ASCII;
			}
			if (In[0]==0x0a || In[0]==0x0d) {
				State2=STATE2_NONE;
			}
			*Out=In[0];
			Out++;
			NextCodec->Len++;
			Len++;
			In++;
			continue;
		} else {
			/* We've got a UTF-8 char */

			if ((Codec->Len-Len)>=4) {
				const Summary16 *summary = NULL;

				CheckUTFInStream;

				/* Decode the UTF char */
				UTF8DecodeChar(In, wc, Len, Codec->Len);

				if (wc >= 0x0000 && wc < 0x0460) {
					summary = &KSC5601_CP00[(wc>>4)];
				} else if (wc >= 0x2000 && wc < 0x2670) {
					summary = &KSC5601_CP20[(wc>>4)-0x200];
				} else if (wc >= 0x3000 && wc < 0x33e0) {
					summary = &KSC5601_CP30[(wc>>4)-0x300];
				} else if (wc >= 0x4e00 && wc < 0x9fa0) {
					summary = &KSC5601_CP4e[(wc>>4)-0x4e0];
				} else if (wc >= 0xac00 && wc < 0xd7a0) {
					summary = &KSC5601_CPac[(wc>>4)-0xac0];
				} else if (wc >= 0xf900 && wc < 0xfa10) {
					summary = &KSC5601_CPf9[(wc>>4)-0xf90];
				} else if (wc >= 0xff00 && wc < 0xfff0) {
					summary = &KSC5601_CPff[(wc>>4)-0xff0];
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
						c = KSC5601_Charset[summary->indx + used];

						if ((c>>8<0x80) && ((c & 0xff) < 0x80)) {
							unsigned long	count=(State2 == STATE2_DESIGNATED_KSC5601 ? 0 : 4) + (State1 == STATE_TWOBYTE ? 0 : 1) + 2;

							FlushOutStream(count);

							if (State2 != STATE2_DESIGNATED_KSC5601) {
								Out[0]=ESC;
								Out[1]='$';
								Out[2]=')';
								Out[3]='C';
								Out+=4;
								NextCodec->Len+=4;
								State2=STATE2_DESIGNATED_KSC5601;
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
					}
				}
				/* If we get here we've gotten an illegal sequence, but we've already skipped the input, so move on */
			} else {
				if (Codec->EOS) {
					Len=Codec->Len;
					EndFlushStream;
					return(Len);
				}
				/* Need more data */
				Codec->State=(State2<<8) | State1;
				if (Len >= Codec->Min) {
				    return(Len);
				}

				return(Codec->Len);
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

	Codec->State=(State2<<8) | State1;

        if (Len >= Codec->Min) {
            return(Len);
        }

        return(Codec->Len);
}

#undef STATE2_DESIGNATED_KSC5601
#undef STATE2_NONE
#undef STATE_TWOBYTE
#undef STATE_ASCII
