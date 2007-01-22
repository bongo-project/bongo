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


EXPORT const unsigned char LeftHexChar[] = {
/*	0x00	000	NUL	Null								*/	0, 
/*	0x01	001	SOH	Start of Heading				*/	0,
/*	0x02	002	STX	Start Text						*/	0,
/*	0x03	003	ETX	End Text							*/	0,
/*	0x04	004	EOT	End of Transmisison			*/	0,
/*	0x05	005	ENQ	Enquiry							*/	0,
/*	0x06	006	ACK	Acknowledge						*/	0,
/*	0x07	007	BEL	Bell								*/	0,
/*	0x08	008	BS		Backspace						*/	0,
/*	0x09	009	HT		Horizontal Tab					*/	0,
/*	0x0A	010	LF		Linefeed							*/	0,
/*	0x0B	011	VT		Vertical Tab					*/	0,
/*	0x0C	012	FF		Formfeed							*/	0,
/*	0x0D	013	CR		Carriage Return				*/	0,
/*	0x0E	014	SO		Shift Out						*/	0,
/*	0x0F	015	SI		Shift In							*/	0,
/*	0x10	016	DLE	Data Link Escape				*/	0,
/*	0x11	017	DC1	Device Control 1				*/	0,
/*	0x12	018	DC2	Device Control 2				*/	0,
/*	0x13	019	DC3	Device Control 3				*/	0,
/*	0x14	020	DC4	Device Control 4				*/	0,
/*	0x15	021	NAK	Negative Acknowledgement	*/	0,
/*	0x16	022	SYN	Synchronous Idle				*/	0,
/*	0x17	023	ETB	End Transmission Block		*/	0,
/*	0x18	024	CAN	Cancel							*/	0,
/*	0x19	025	EM		End Medium						*/	0,
/*	0x1A	026	EOF	End of File						*/	0,
/*	0x1B	027	ESC	Escape							*/	0,
/*	0x1C	028	FS		File Separator					*/	0,
/*	0x1D	029	GS		Group Separator				*/	0,
/*	0x1E	030	HOM	Home								*/	0,
/*	0x1F	031	NEW	Newline							*/	0,
/*	0x20	032	SPA	Space								*/	0,
/*	0x21	033	!		Exclamation Point				*/	0,
/*	0x22	034	"		Double Quote					*/	0,
/*	0x23	035	#		Pound Sign						*/	0,
/*	0x24	036	$		Dollar Sign						*/	0,
/*	0x25	037	%		Percent							*/	0,
/*	0x26	038	&		Ampersand						*/	0,
/*	0x27	039	'		Apostrophe						*/	0,
/*	0x28	040	(		Right Parenthesis				*/	0,
/*	0x29	041	)		Left Parenthesis				*/	0,
/*	0x2A	042	*		Asterick							*/	0,
/*	0x2B	043	+		Plus								*/	0,
/*	0x2C	044	,		Comma								*/	0,
/*	0x2D	045	-		Minus								*/	0,
/*	0x2E	046	.		Period							*/	0,
/*	0x2F	047	/		Forward Slash					*/	0,
/*	0x30	048	0		Zero								*/	0,
/*	0x31	049	1		One								*/	16,
/*	0x32	050	2		Two								*/	32,
/*	0x33	051	3		Three								*/	48,
/*	0x34	052	4		Four								*/	64,
/*	0x35	053	5		Five								*/	80,
/*	0x36	054	6		Six								*/	96,
/*	0x37	055	7		Seven								*/	112,
/*	0x38	056	8		Eight								*/	128,
/*	0x39	057	9		Nine								*/	144,
/*	0x3A	058	:		Colon								*/	0,
/*	0x3B	059	;		Semi-Colon						*/	0,
/*	0x3C	060	<		Less-than						*/	0,
/*	0x3D	061	=		Equal								*/	0,
/*	0x3E	062	>		Greater-than					*/	0,
/*	0x3F	063	?		Question Mark					*/	0,
/*	0x40	064	@		At									*/	0,
/*	0x41	065	A		Uppercase A						*/	160,
/*	0x42	066	B		Uppercase B						*/	176,
/*	0x43	067	C		Uppercase C						*/	192,
/*	0x44	068	D		Uppercase D						*/	208,
/*	0x45	069	E		Uppercase E						*/	224,
/*	0x46	070	F		Uppercase F						*/	240,
/*	0x47	071	G		Uppercase G						*/	0,
/*	0x48	072	H		Uppercase H						*/	0,
/*	0x49	073	I		Uppercase I						*/	0,
/*	0x4A	074	J		Uppercase J						*/	0,
/*	0x4B	075	K		Uppercase K						*/	0,
/*	0x4C	076	L		Uppercase L						*/	0,
/*	0x4D	077	M		Uppercase M						*/	0,
/*	0x4E	078	N		Uppercase N						*/	0,
/*	0x4F	079	O		Uppercase O						*/	0,
/*	0x50	080	P		Uppercase P						*/	0,
/*	0x51	081	Q		Uppercase Q						*/	0,
/*	0x52	082	R		Uppercase R						*/	0,
/*	0x53	083	S		Uppercase S						*/	0,
/*	0x54	084	T		Uppercase T						*/	0,
/*	0x55	085	U		Uppercase U						*/	0,
/*	0x56	086	V		Uppercase V						*/	0,
/*	0x57	087	W		Uppercase W						*/	0,
/*	0x58	088	X		Uppercase X						*/	0,
/*	0x59	089	Y		Uppercase Y						*/	0,
/*	0x5A	090	Z		Uppercase Z						*/	0,
/*	0x5B	091	[											*/	0,
/*	0x5C	092	\		Back Slash						*/	0,
/*	0x5D	093	]											*/	0,
/*	0x5E	094	^											*/	0,
/*	0x5F	095	_		Underscore						*/	0,
/*	0x60	096	`											*/	0,
/*	0x61	097	a		Lowercase a						*/	160,
/*	0x62	098	b		Lowercase b						*/	176,
/*	0x63	099	c		Lowercase c						*/	192,
/*	0x64	100	d		Lowercase d						*/	208,
/*	0x65	101	e		Lowercase e						*/	224,
/*	0x66	102	f		Lowercase f						*/	240,
/*	0x67	103	g		Lowercase g						*/	0,
/*	0x68	104	h		Lowercase h						*/	0,
/*	0x69	105	i		Lowercase i						*/	0,
/*	0x6A	106	j		Lowercase j						*/	0,
/*	0x6B	107	k		Lowercase k						*/	0,
/*	0x6C	108	l		Lowercase l						*/	0,
/*	0x6D	109	m		Lowercase m						*/	0,
/*	0x6E	110	n		Lowercase n						*/	0,
/*	0x6F	111	o		Lowercase o						*/	0,
/*	0x70	112	p		Lowercase p						*/	0,
/*	0x71	113	q		Lowercase q						*/	0,
/*	0x72	114	r		Lowercase r						*/	0,
/*	0x73	115	s		Lowercase s						*/	0,
/*	0x74	116	t		Lowercase t						*/	0,
/*	0x75	117	u		Lowercase u						*/	0,
/*	0x76	118	v		Lowercase v						*/	0,
/*	0x77	119	w		Lowercase w						*/	0,
/*	0x78	120	x		Lowercase x						*/	0,
/*	0x79	121	y		Lowercase y						*/	0,
/*	0x7A	122	z		Lowercase z						*/	0,
/*	0x7B	123	{											*/	0,
/*	0x7C	124	|											*/	0,
/*	0x7D	125	}											*/	0,
/*	0x7E	126	~											*/	0,
/*	0x7F	127	DEL										*/	0,
/*	0x80	128												*/	0,
/*	0x81	129												*/	0,
/*	0x82	130												*/	0,
/*	0x83	131												*/	0,
/*	0x84	132												*/	0, 
/*	0x85	133												*/	0, 
/*	0x86	134												*/	0, 
/*	0x87	135												*/	0, 
/*	0x88	136												*/	0, 
/*	0x89	137												*/	0, 
/*	0x8A	138												*/	0, 
/*	0x8B	139												*/	0, 
/*	0x8C	140												*/	0, 
/*	0x8D	141												*/	0, 
/*	0x8E	142												*/	0, 
/*	0x8F	143												*/	0, 
/*	0x90	144												*/	0, 
/*	0x91	145												*/	0, 
/*	0x92	146												*/	0, 
/*	0x93	147												*/	0, 
/*	0x94	148												*/	0, 
/*	0x95	149												*/	0, 
/*	0x96	150												*/	0, 
/*	0x97	151												*/	0, 
/*	0x98	152												*/	0, 
/*	0x99	153												*/	0, 
/*	0x9A	154												*/	0, 
/*	0x9B	155												*/	0, 
/*	0x9C	156												*/	0, 
/*	0x9D	157												*/	0, 
/*	0x9E	158												*/	0, 
/*	0x9F	159												*/	0, 
/*	0xA0	160												*/	0, 
/*	0xA1	161												*/	0, 
/*	0xA2	162												*/	0, 
/*	0xA3	163												*/	0, 
/*	0xA4	164												*/	0, 
/*	0xA5	165												*/	0, 
/*	0xA6	166												*/	0, 
/*	0xA7	167												*/	0, 
/*	0xA8	168												*/	0, 
/*	0xA9	169												*/	0, 
/*	0xAA	170												*/	0, 
/*	0xAB	171												*/	0, 
/*	0xAC	172												*/	0, 
/*	0xAD	173												*/	0, 
/*	0xAE	174												*/	0, 
/*	0xAF	175												*/	0, 
/*	0xB0	176												*/	0, 
/*	0xB1	177												*/	0, 
/*	0xB2	178												*/	0, 
/*	0xB3	179												*/	0, 
/*	0xB4	180												*/	0, 
/*	0xB5	181												*/	0, 
/*	0xB6	182												*/	0, 
/*	0xB7	183												*/	0, 
/*	0xB8	184												*/	0, 
/*	0xB9	185												*/	0, 
/*	0xBA	186												*/	0, 
/*	0xBB	187												*/	0, 
/*	0xBC	188												*/	0, 
/*	0xBD	189												*/	0, 
/*	0xBE	190												*/	0, 
/*	0xBF	191												*/	0, 
/*	0xC0	192												*/	0, 
/*	0xC1	193												*/	0, 
/*	0xC2	194												*/	0, 
/*	0xC3	195												*/	0, 
/*	0xC4	196												*/	0, 
/*	0xC5	197												*/	0, 
/*	0xC6	198												*/	0, 
/*	0xC7	199												*/	0, 
/*	0xC8	200												*/	0, 
/*	0xC9	201												*/	0, 
/*	0xCA	202												*/	0, 
/*	0xCB	203												*/	0, 
/*	0xCC	204												*/	0, 
/*	0xCD	205												*/	0, 
/*	0xCE	206												*/	0, 
/*	0xCF	207												*/	0, 
/*	0xD0	208												*/	0, 
/*	0xD1	209												*/	0, 
/*	0xD2	210												*/	0, 
/*	0xD3	211												*/	0, 
/*	0xD4	212												*/	0, 
/*	0xD5	213												*/	0, 
/*	0xD6	214												*/	0, 
/*	0xD7	215												*/	0, 
/*	0xD8	216												*/	0, 
/*	0xD9	217												*/	0, 
/*	0xDA	218												*/	0, 
/*	0xDB	219												*/	0, 
/*	0xDC	220												*/	0, 
/*	0xDD	221												*/	0, 
/*	0xDE	222												*/	0, 
/*	0xDF	223												*/	0, 
/*	0xE0	224												*/	0, 
/*	0xE1	225												*/	0, 
/*	0xE2	226												*/	0, 
/*	0xE3	227												*/	0, 
/*	0xE4	228												*/	0, 
/*	0xE5	229												*/	0, 
/*	0xE6	230												*/	0, 
/*	0xE7	231												*/	0, 
/*	0xE8	232												*/	0, 
/*	0xE9	233												*/	0, 
/*	0xEA	234												*/	0, 
/*	0xEB	235												*/	0, 
/*	0xEC	236												*/	0, 
/*	0xED	237												*/	0, 
/*	0xEE	238												*/	0, 
/*	0xEF	239												*/	0, 
/*	0xF0	240												*/	0, 
/*	0xF1	241												*/	0, 
/*	0xF2	242												*/	0, 
/*	0xF3	243												*/	0, 
/*	0xF4	244												*/	0, 
/*	0xF5	245												*/	0, 
/*	0xF6	246												*/	0, 
/*	0xF7	247												*/	0, 
/*	0xF8	248												*/	0, 
/*	0xF9	249												*/	0, 
/*	0xFA	250												*/	0, 
/*	0xFB	251												*/	0, 
/*	0xFC	252												*/	0, 
/*	0xFD	253												*/	0, 
/*	0xFE	254												*/	0, 
/*	0xFF	255												*/	0
};


EXPORT const unsigned char RightHexChar[] = {
/*	0x00	000	NUL	Null								*/	0, 
/*	0x01	001	SOH	Start of Heading				*/	0,
/*	0x02	002	STX	Start Text						*/	0,
/*	0x03	003	ETX	End Text							*/	0,
/*	0x04	004	EOT	End of Transmisison			*/	0,
/*	0x05	005	ENQ	Enquiry							*/	0,
/*	0x06	006	ACK	Acknowledge						*/	0,
/*	0x07	007	BEL	Bell								*/	0,
/*	0x08	008	BS		Backspace						*/	0,
/*	0x09	009	HT		Horizontal Tab					*/	0,
/*	0x0A	010	LF		Linefeed							*/	0,
/*	0x0B	011	VT		Vertical Tab					*/	0,
/*	0x0C	012	FF		Formfeed							*/	0,
/*	0x0D	013	CR		Carriage Return				*/	0,
/*	0x0E	014	SO		Shift Out						*/	0,
/*	0x0F	015	SI		Shift In							*/	0,
/*	0x10	016	DLE	Data Link Escape				*/	0,
/*	0x11	017	DC1	Device Control 1				*/	0,
/*	0x12	018	DC2	Device Control 2				*/	0,
/*	0x13	019	DC3	Device Control 3				*/	0,
/*	0x14	020	DC4	Device Control 4				*/	0,
/*	0x15	021	NAK	Negative Acknowledgement	*/	0,
/*	0x16	022	SYN	Synchronous Idle				*/	0,
/*	0x17	023	ETB	End Transmission Block		*/	0,
/*	0x18	024	CAN	Cancel							*/	0,
/*	0x19	025	EM		End Medium						*/	0,
/*	0x1A	026	EOF	End of File						*/	0,
/*	0x1B	027	ESC	Escape							*/	0,
/*	0x1C	028	FS		File Separator					*/	0,
/*	0x1D	029	GS		Group Separator				*/	0,
/*	0x1E	030	HOM	Home								*/	0,
/*	0x1F	031	NEW	Newline							*/	0,
/*	0x20	032	SPA	Space								*/	0,
/*	0x21	033	!		Exclamation Point				*/	0,
/*	0x22	034	"		Double Quote					*/	0,
/*	0x23	035	#		Pound Sign						*/	0,
/*	0x24	036	$		Dollar Sign						*/	0,
/*	0x25	037	%		Percent							*/	0,
/*	0x26	038	&		Ampersand						*/	0,
/*	0x27	039	'		Apostrophe						*/	0,
/*	0x28	040	(		Right Parenthesis				*/	0,
/*	0x29	041	)		Left Parenthesis				*/	0,
/*	0x2A	042	*		Asterick							*/	0,
/*	0x2B	043	+		Plus								*/	0,
/*	0x2C	044	,		Comma								*/	0,
/*	0x2D	045	-		Minus								*/	0,
/*	0x2E	046	.		Period							*/	0,
/*	0x2F	047	/		Forward Slash					*/	0,
/*	0x30	048	0		Zero								*/	0,
/*	0x31	049	1		One								*/	1,
/*	0x32	050	2		Two								*/	2,
/*	0x33	051	3		Three								*/	3,
/*	0x34	052	4		Four								*/	4,
/*	0x35	053	5		Five								*/	5,
/*	0x36	054	6		Six								*/	6,
/*	0x37	055	7		Seven								*/	7,
/*	0x38	056	8		Eight								*/	8,
/*	0x39	057	9		Nine								*/	9,
/*	0x3A	058	:		Colon								*/	0,
/*	0x3B	059	;		Semi-Colon						*/	0,
/*	0x3C	060	<		Less-than						*/	0,
/*	0x3D	061	=		Equal								*/	0,
/*	0x3E	062	>		Greater-than					*/	0,
/*	0x3F	063	?		Question Mark					*/	0,
/*	0x40	064	@		At									*/	0,
/*	0x41	065	A		Uppercase A						*/	10,
/*	0x42	066	B		Uppercase B						*/	11,
/*	0x43	067	C		Uppercase C						*/	12,
/*	0x44	068	D		Uppercase D						*/	13,
/*	0x45	069	E		Uppercase E						*/	14,
/*	0x46	070	F		Uppercase F						*/	15,
/*	0x47	071	G		Uppercase G						*/	0,
/*	0x48	072	H		Uppercase H						*/	0,
/*	0x49	073	I		Uppercase I						*/	0,
/*	0x4A	074	J		Uppercase J						*/	0,
/*	0x4B	075	K		Uppercase K						*/	0,
/*	0x4C	076	L		Uppercase L						*/	0,
/*	0x4D	077	M		Uppercase M						*/	0,
/*	0x4E	078	N		Uppercase N						*/	0,
/*	0x4F	079	O		Uppercase O						*/	0,
/*	0x50	080	P		Uppercase P						*/	0,
/*	0x51	081	Q		Uppercase Q						*/	0,
/*	0x52	082	R		Uppercase R						*/	0,
/*	0x53	083	S		Uppercase S						*/	0,
/*	0x54	084	T		Uppercase T						*/	0,
/*	0x55	085	U		Uppercase U						*/	0,
/*	0x56	086	V		Uppercase V						*/	0,
/*	0x57	087	W		Uppercase W						*/	0,
/*	0x58	088	X		Uppercase X						*/	0,
/*	0x59	089	Y		Uppercase Y						*/	0,
/*	0x5A	090	Z		Uppercase Z						*/	0,
/*	0x5B	091	[											*/	0,
/*	0x5C	092	\		Back Slash						*/	0,
/*	0x5D	093	]											*/	0,
/*	0x5E	094	^											*/	0,
/*	0x5F	095	_		Underscore						*/	0,
/*	0x60	096	`											*/	0,
/*	0x61	097	a		Lowercase a						*/	10,
/*	0x62	098	b		Lowercase b						*/	11,
/*	0x63	099	c		Lowercase c						*/	12,
/*	0x64	100	d		Lowercase d						*/	13,
/*	0x65	101	e		Lowercase e						*/	14,
/*	0x66	102	f		Lowercase f						*/	15,
/*	0x67	103	g		Lowercase g						*/	0,
/*	0x68	104	h		Lowercase h						*/	0,
/*	0x69	105	i		Lowercase i						*/	0,
/*	0x6A	106	j		Lowercase j						*/	0,
/*	0x6B	107	k		Lowercase k						*/	0,
/*	0x6C	108	l		Lowercase l						*/	0,
/*	0x6D	109	m		Lowercase m						*/	0,
/*	0x6E	110	n		Lowercase n						*/	0,
/*	0x6F	111	o		Lowercase o						*/	0,
/*	0x70	112	p		Lowercase p						*/	0,
/*	0x71	113	q		Lowercase q						*/	0,
/*	0x72	114	r		Lowercase r						*/	0,
/*	0x73	115	s		Lowercase s						*/	0,
/*	0x74	116	t		Lowercase t						*/	0,
/*	0x75	117	u		Lowercase u						*/	0,
/*	0x76	118	v		Lowercase v						*/	0,
/*	0x77	119	w		Lowercase w						*/	0,
/*	0x78	120	x		Lowercase x						*/	0,
/*	0x79	121	y		Lowercase y						*/	0,
/*	0x7A	122	z		Lowercase z						*/	0,
/*	0x7B	123	{											*/	0,
/*	0x7C	124	|											*/	0,
/*	0x7D	125	}											*/	0,
/*	0x7E	126	~											*/	0,
/*	0x7F	127	DEL										*/	0,
/*	0x80	128												*/	0,
/*	0x81	129												*/	0,
/*	0x82	130												*/	0,
/*	0x83	131												*/	0,
/*	0x84	132												*/	0, 
/*	0x85	133												*/	0, 
/*	0x86	134												*/	0, 
/*	0x87	135												*/	0, 
/*	0x88	136												*/	0, 
/*	0x89	137												*/	0, 
/*	0x8A	138												*/	0, 
/*	0x8B	139												*/	0, 
/*	0x8C	140												*/	0, 
/*	0x8D	141												*/	0, 
/*	0x8E	142												*/	0, 
/*	0x8F	143												*/	0, 
/*	0x90	144												*/	0, 
/*	0x91	145												*/	0, 
/*	0x92	146												*/	0, 
/*	0x93	147												*/	0, 
/*	0x94	148												*/	0, 
/*	0x95	149												*/	0, 
/*	0x96	150												*/	0, 
/*	0x97	151												*/	0, 
/*	0x98	152												*/	0, 
/*	0x99	153												*/	0, 
/*	0x9A	154												*/	0, 
/*	0x9B	155												*/	0, 
/*	0x9C	156												*/	0, 
/*	0x9D	157												*/	0, 
/*	0x9E	158												*/	0, 
/*	0x9F	159												*/	0, 
/*	0xA0	160												*/	0, 
/*	0xA1	161												*/	0, 
/*	0xA2	162												*/	0, 
/*	0xA3	163												*/	0, 
/*	0xA4	164												*/	0, 
/*	0xA5	165												*/	0, 
/*	0xA6	166												*/	0, 
/*	0xA7	167												*/	0, 
/*	0xA8	168												*/	0, 
/*	0xA9	169												*/	0, 
/*	0xAA	170												*/	0, 
/*	0xAB	171												*/	0, 
/*	0xAC	172												*/	0, 
/*	0xAD	173												*/	0, 
/*	0xAE	174												*/	0, 
/*	0xAF	175												*/	0, 
/*	0xB0	176												*/	0, 
/*	0xB1	177												*/	0, 
/*	0xB2	178												*/	0, 
/*	0xB3	179												*/	0, 
/*	0xB4	180												*/	0, 
/*	0xB5	181												*/	0, 
/*	0xB6	182												*/	0, 
/*	0xB7	183												*/	0, 
/*	0xB8	184												*/	0, 
/*	0xB9	185												*/	0, 
/*	0xBA	186												*/	0, 
/*	0xBB	187												*/	0, 
/*	0xBC	188												*/	0, 
/*	0xBD	189												*/	0, 
/*	0xBE	190												*/	0, 
/*	0xBF	191												*/	0, 
/*	0xC0	192												*/	0, 
/*	0xC1	193												*/	0, 
/*	0xC2	194												*/	0, 
/*	0xC3	195												*/	0, 
/*	0xC4	196												*/	0, 
/*	0xC5	197												*/	0, 
/*	0xC6	198												*/	0, 
/*	0xC7	199												*/	0, 
/*	0xC8	200												*/	0, 
/*	0xC9	201												*/	0, 
/*	0xCA	202												*/	0, 
/*	0xCB	203												*/	0, 
/*	0xCC	204												*/	0, 
/*	0xCD	205												*/	0, 
/*	0xCE	206												*/	0, 
/*	0xCF	207												*/	0, 
/*	0xD0	208												*/	0, 
/*	0xD1	209												*/	0, 
/*	0xD2	210												*/	0, 
/*	0xD3	211												*/	0, 
/*	0xD4	212												*/	0, 
/*	0xD5	213												*/	0, 
/*	0xD6	214												*/	0, 
/*	0xD7	215												*/	0, 
/*	0xD8	216												*/	0, 
/*	0xD9	217												*/	0, 
/*	0xDA	218												*/	0, 
/*	0xDB	219												*/	0, 
/*	0xDC	220												*/	0, 
/*	0xDD	221												*/	0, 
/*	0xDE	222												*/	0, 
/*	0xDF	223												*/	0, 
/*	0xE0	224												*/	0, 
/*	0xE1	225												*/	0, 
/*	0xE2	226												*/	0, 
/*	0xE3	227												*/	0, 
/*	0xE4	228												*/	0, 
/*	0xE5	229												*/	0, 
/*	0xE6	230												*/	0, 
/*	0xE7	231												*/	0, 
/*	0xE8	232												*/	0, 
/*	0xE9	233												*/	0, 
/*	0xEA	234												*/	0, 
/*	0xEB	235												*/	0, 
/*	0xEC	236												*/	0, 
/*	0xED	237												*/	0, 
/*	0xEE	238												*/	0, 
/*	0xEF	239												*/	0, 
/*	0xF0	240												*/	0, 
/*	0xF1	241												*/	0, 
/*	0xF2	242												*/	0, 
/*	0xF3	243												*/	0, 
/*	0xF4	244												*/	0, 
/*	0xF5	245												*/	0, 
/*	0xF6	246												*/	0, 
/*	0xF7	247												*/	0, 
/*	0xF8	248												*/	0, 
/*	0xF9	249												*/	0, 
/*	0xFA	250												*/	0, 
/*	0xFB	251												*/	0, 
/*	0xFC	252												*/	0, 
/*	0xFD	253												*/	0, 
/*	0xFE	254												*/	0, 
/*	0xFF	255												*/	0
};

EXPORT const unsigned char AttributeChar[] = {
/*	0x00	000	NUL	Null								*/	0, 
/*	0x01	001	SOH	Start of Heading				*/	0,
/*	0x02	002	STX	Start Text						*/	0,
/*	0x03	003	ETX	End Text							*/	0,
/*	0x04	004	EOT	End of Transmisison			*/	0,
/*	0x05	005	ENQ	Enquiry							*/	0,
/*	0x06	006	ACK	Acknowledge						*/	0,
/*	0x07	007	BEL	Bell								*/	0,
/*	0x08	008	BS		Backspace						*/	0,
/*	0x09	009	HT		Horizontal Tab					*/	0,
/*	0x0A	010	LF		Linefeed							*/	0,
/*	0x0B	011	VT		Vertical Tab					*/	0,
/*	0x0C	012	FF		Formfeed							*/	0,
/*	0x0D	013	CR		Carriage Return				*/	0,
/*	0x0E	014	SO		Shift Out						*/	0,
/*	0x0F	015	SI		Shift In							*/	0,
/*	0x10	016	DLE	Data Link Escape				*/	0,
/*	0x11	017	DC1	Device Control 1				*/	0,
/*	0x12	018	DC2	Device Control 2				*/	0,
/*	0x13	019	DC3	Device Control 3				*/	0,
/*	0x14	020	DC4	Device Control 4				*/	0,
/*	0x15	021	NAK	Negative Acknowledgement	*/	0,
/*	0x16	022	SYN	Synchronous Idle				*/	0,
/*	0x17	023	ETB	End Transmission Block		*/	0,
/*	0x18	024	CAN	Cancel							*/	0,
/*	0x19	025	EM		End Medium						*/	0,
/*	0x1A	026	EOF	End of File						*/	0,
/*	0x1B	027	ESC	Escape							*/	0,
/*	0x1C	028	FS		File Separator					*/	0,
/*	0x1D	029	GS		Group Separator				*/	0,
/*	0x1E	030	HOM	Home								*/	0,
/*	0x1F	031	NEW	Newline							*/	0,
/*	0x20	032	SPA	Space								*/	0,
/*	0x21	033	!		Exclamation Point				*/	1,
/*	0x22	034	"		Double Quote					*/	0,
/*	0x23	035	#		Pound Sign						*/	1,
/*	0x24	036	$		Dollar Sign						*/	1,
/*	0x25	037	%		Percent							*/	0,
/*	0x26	038	&		Ampersand						*/	1,
/*	0x27	039	'		Apostrophe						*/	0,
/*	0x28	040	(		Right Parenthesis				*/	0,
/*	0x29	041	)		Left Parenthesis				*/	0,
/*	0x2A	042	*		Asterick							*/	0,
/*	0x2B	043	+		Plus								*/	1,
/*	0x2C	044	,		Comma								*/	0,
/*	0x2D	045	-		Minus								*/	1,
/*	0x2E	046	.		Period							*/	1,
/*	0x2F	047	/		Forward Slash					*/	0,
/*	0x30	048	0		Zero								*/	1,
/*	0x31	049	1		One								*/	1,
/*	0x32	050	2		Two								*/	1,
/*	0x33	051	3		Three								*/	1,
/*	0x34	052	4		Four								*/	1,
/*	0x35	053	5		Five								*/	1,
/*	0x36	054	6		Six								*/	1,
/*	0x37	055	7		Seven								*/	1,
/*	0x38	056	8		Eight								*/	1,
/*	0x39	057	9		Nine								*/	1,
/*	0x3A	058	:		Colon								*/	0,
/*	0x3B	059	;		Semi-Colon						*/	0,
/*	0x3C	060	<		Less-than						*/	0,
/*	0x3D	061	=		Equal								*/	0,
/*	0x3E	062	>		Greater-than					*/	0,
/*	0x3F	063	?		Question Mark					*/	0,
/*	0x40	064	@		At									*/	0,
/*	0x41	065	A		Uppercase A						*/	1,
/*	0x42	066	B		Uppercase B						*/	1,
/*	0x43	067	C		Uppercase C						*/	1,
/*	0x44	068	D		Uppercase D						*/	1,
/*	0x45	069	E		Uppercase E						*/	1,
/*	0x46	070	F		Uppercase F						*/	1,
/*	0x47	071	G		Uppercase G						*/	1,
/*	0x48	072	H		Uppercase H						*/	1,
/*	0x49	073	I		Uppercase I						*/	1,
/*	0x4A	074	J		Uppercase J						*/	1,
/*	0x4B	075	K		Uppercase K						*/	1,
/*	0x4C	076	L		Uppercase L						*/	1,
/*	0x4D	077	M		Uppercase M						*/	1,
/*	0x4E	078	N		Uppercase N						*/	1,
/*	0x4F	079	O		Uppercase O						*/	1,
/*	0x50	080	P		Uppercase P						*/	1,
/*	0x51	081	Q		Uppercase Q						*/	1,
/*	0x52	082	R		Uppercase R						*/	1,
/*	0x53	083	S		Uppercase S						*/	1,
/*	0x54	084	T		Uppercase T						*/	1,
/*	0x55	085	U		Uppercase U						*/	1,
/*	0x56	086	V		Uppercase V						*/	1,
/*	0x57	087	W		Uppercase W						*/	1,
/*	0x58	088	X		Uppercase X						*/	1,
/*	0x59	089	Y		Uppercase Y						*/	1,
/*	0x5A	090	Z		Uppercase Z						*/	1,
/*	0x5B	091	[											*/	0,
/*	0x5C	092	\		Back Slash						*/	0,
/*	0x5D	093	]											*/	0,
/*	0x5E	094	^											*/	1,
/*	0x5F	095	_		Underscore						*/	1,
/*	0x60	096	`											*/	1,
/*	0x61	097	a		Lowercase a						*/	1,
/*	0x62	098	b		Lowercase b						*/	1,
/*	0x63	099	c		Lowercase c						*/	1,
/*	0x64	100	d		Lowercase d						*/	1,
/*	0x65	101	e		Lowercase e						*/	1,
/*	0x66	102	f		Lowercase f						*/	1,
/*	0x67	103	g		Lowercase g						*/	1,
/*	0x68	104	h		Lowercase h						*/	1,
/*	0x69	105	i		Lowercase i						*/	1,
/*	0x6A	106	j		Lowercase j						*/	1,
/*	0x6B	107	k		Lowercase k						*/	1,
/*	0x6C	108	l		Lowercase l						*/	1,
/*	0x6D	109	m		Lowercase m						*/	1,
/*	0x6E	110	n		Lowercase n						*/	1,
/*	0x6F	111	o		Lowercase o						*/	1,
/*	0x70	112	p		Lowercase p						*/	1,
/*	0x71	113	q		Lowercase q						*/	1,
/*	0x72	114	r		Lowercase r						*/	1,
/*	0x73	115	s		Lowercase s						*/	1,
/*	0x74	116	t		Lowercase t						*/	1,
/*	0x75	117	u		Lowercase u						*/	1,
/*	0x76	118	v		Lowercase v						*/	1,
/*	0x77	119	w		Lowercase w						*/	1,
/*	0x78	120	x		Lowercase x						*/	1,
/*	0x79	121	y		Lowercase y						*/	1,
/*	0x7A	122	z		Lowercase z						*/	1,
/*	0x7B	123	{											*/	1,
/*	0x7C	124	|											*/	1,
/*	0x7D	125	}											*/	1,
/*	0x7E	126	~											*/	1,
/*	0x7F	127	DEL										*/	0,
/*	0x80	128												*/	0,
/*	0x81	129												*/	0,
/*	0x82	130												*/	0,
/*	0x83	131												*/	0,
/*	0x84	132												*/	0, 
/*	0x85	133												*/	0, 
/*	0x86	134												*/	0, 
/*	0x87	135												*/	0, 
/*	0x88	136												*/	0, 
/*	0x89	137												*/	0, 
/*	0x8A	138												*/	0, 
/*	0x8B	139												*/	0, 
/*	0x8C	140												*/	0, 
/*	0x8D	141												*/	0, 
/*	0x8E	142												*/	0, 
/*	0x8F	143												*/	0, 
/*	0x90	144												*/	0, 
/*	0x91	145												*/	0, 
/*	0x92	146												*/	0, 
/*	0x93	147												*/	0, 
/*	0x94	148												*/	0, 
/*	0x95	149												*/	0, 
/*	0x96	150												*/	0, 
/*	0x97	151												*/	0, 
/*	0x98	152												*/	0, 
/*	0x99	153												*/	0, 
/*	0x9A	154												*/	0, 
/*	0x9B	155												*/	0, 
/*	0x9C	156												*/	0, 
/*	0x9D	157												*/	0, 
/*	0x9E	158												*/	0, 
/*	0x9F	159												*/	0, 
/*	0xA0	160												*/	0, 
/*	0xA1	161												*/	0, 
/*	0xA2	162												*/	0, 
/*	0xA3	163												*/	0, 
/*	0xA4	164												*/	0, 
/*	0xA5	165												*/	0, 
/*	0xA6	166												*/	0, 
/*	0xA7	167												*/	0, 
/*	0xA8	168												*/	0, 
/*	0xA9	169												*/	0, 
/*	0xAA	170												*/	0, 
/*	0xAB	171												*/	0, 
/*	0xAC	172												*/	0, 
/*	0xAD	173												*/	0, 
/*	0xAE	174												*/	0, 
/*	0xAF	175												*/	0, 
/*	0xB0	176												*/	0, 
/*	0xB1	177												*/	0, 
/*	0xB2	178												*/	0, 
/*	0xB3	179												*/	0, 
/*	0xB4	180												*/	0, 
/*	0xB5	181												*/	0, 
/*	0xB6	182												*/	0, 
/*	0xB7	183												*/	0, 
/*	0xB8	184												*/	0, 
/*	0xB9	185												*/	0, 
/*	0xBA	186												*/	0, 
/*	0xBB	187												*/	0, 
/*	0xBC	188												*/	0, 
/*	0xBD	189												*/	0, 
/*	0xBE	190												*/	0, 
/*	0xBF	191												*/	0, 
/*	0xC0	192												*/	0, 
/*	0xC1	193												*/	0, 
/*	0xC2	194												*/	0, 
/*	0xC3	195												*/	0, 
/*	0xC4	196												*/	0, 
/*	0xC5	197												*/	0, 
/*	0xC6	198												*/	0, 
/*	0xC7	199												*/	0, 
/*	0xC8	200												*/	0, 
/*	0xC9	201												*/	0, 
/*	0xCA	202												*/	0, 
/*	0xCB	203												*/	0, 
/*	0xCC	204												*/	0, 
/*	0xCD	205												*/	0, 
/*	0xCE	206												*/	0, 
/*	0xCF	207												*/	0, 
/*	0xD0	208												*/	0, 
/*	0xD1	209												*/	0, 
/*	0xD2	210												*/	0, 
/*	0xD3	211												*/	0, 
/*	0xD4	212												*/	0, 
/*	0xD5	213												*/	0, 
/*	0xD6	214												*/	0, 
/*	0xD7	215												*/	0, 
/*	0xD8	216												*/	0, 
/*	0xD9	217												*/	0, 
/*	0xDA	218												*/	0, 
/*	0xDB	219												*/	0, 
/*	0xDC	220												*/	0, 
/*	0xDD	221												*/	0, 
/*	0xDE	222												*/	0, 
/*	0xDF	223												*/	0, 
/*	0xE0	224												*/	0, 
/*	0xE1	225												*/	0, 
/*	0xE2	226												*/	0, 
/*	0xE3	227												*/	0, 
/*	0xE4	228												*/	0, 
/*	0xE5	229												*/	0, 
/*	0xE6	230												*/	0, 
/*	0xE7	231												*/	0, 
/*	0xE8	232												*/	0, 
/*	0xE9	233												*/	0, 
/*	0xEA	234												*/	0, 
/*	0xEB	235												*/	0, 
/*	0xEC	236												*/	0, 
/*	0xED	237												*/	0, 
/*	0xEE	238												*/	0, 
/*	0xEF	239												*/	0, 
/*	0xF0	240												*/	0, 
/*	0xF1	241												*/	0, 
/*	0xF2	242												*/	0, 
/*	0xF3	243												*/	0, 
/*	0xF4	244												*/	0, 
/*	0xF5	245												*/	0, 
/*	0xF6	246												*/	0, 
/*	0xF7	247												*/	0, 
/*	0xF8	248												*/	0, 
/*	0xF9	249												*/	0, 
/*	0xFA	250												*/	0, 
/*	0xFB	251												*/	0, 
/*	0xFC	252												*/	0, 
/*	0xFD	253												*/	0, 
/*	0xFE	254												*/	0, 
/*	0xFF	255												*/	0
};


EXPORT const unsigned char HexChar[] = {
/*	0x00	000	0											*/	'0', 
/*	0x01	001	1											*/	'1',
/*	0x02	002	2											*/	'2',
/*	0x03	003	3											*/	'3',
/*	0x04	004	4											*/	'4',
/*	0x05	005	5											*/	'5',
/*	0x06	006	6											*/	'6',
/*	0x07	007	7											*/	'7',
/*	0x08	008	8											*/	'8',
/*	0x09	009	9											*/	'9',
/*	0x0A	010	A											*/	'A',
/*	0x0B	011	B											*/	'B',
/*	0x0C	012	C											*/	'C',
/*	0x0D	013	D											*/	'D',
/*	0x0E	014	E											*/	'E',
/*	0x0F	015	F											*/	'F'
};

/* This codec takes a path in the input stream and passes only the filename to the output stream */
/* Codec->Start is required to point to the beginning of the path */
/* Codec->EOS is required, meaning the whole path is required to be in the stream */

static int
Filename_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned long	Len = 0;

	if (Codec->EOS) {
		unsigned long count;
		unsigned long processed;
		unsigned char *fileName;
		unsigned char *In			= Codec->Start;
		unsigned char *InEnd		= Codec->Start + Codec->Len;
		unsigned char *Out		= NextCodec->Start + NextCodec->Len;
		unsigned char *OutEnd	= NextCodec->End;

		fileName = InEnd - 1;

		while (fileName >= Codec->Start) {
			if ((*fileName != '\\') && (*fileName != '/')) {
				fileName--;
				continue;
			}
			fileName++;													
			break;
		}

		if (fileName >= Codec->Start) {
			;
		} else {
			fileName = Codec->Start;
		}

		count = InEnd - fileName;
		processed = 0;

		do {
			if ((count - processed) < BUFSIZE) {
				FlushOutStream(count);
				memcpy(Out, fileName + processed, (count - processed));
				Out += (count - processed);
				NextCodec->Len += (count - processed);
				break;
			}
			FlushOutStream(BUFSIZE - 1);
			memcpy(Out, fileName + processed, BUFSIZE - 1);
			Out += (BUFSIZE - 1);
			NextCodec->Len += (BUFSIZE - 1);
			processed += (BUFSIZE - 1);
		} while (processed < count);


		In += Codec->Len;
		Len += Codec->Len;

		EndFlushStream;
	}

        if (Len >= Codec->Min) {
            return(Len);
        }

        return(Codec->Len);
}

/* This codec takes a filename in the input stream and prepends the path specified by StreamData to the output stream */
/* Codec->Start is required to point to the beginning of the filename */
/* Codec->EOS is required, meaning the whole filename is required to be in the stream */

static int
Filename_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned long	Len = 0;
	unsigned long	processed;

	if (Codec->EOS) {
		unsigned long count;
		unsigned char *In			= Codec->Start;
		unsigned char *Out		= NextCodec->Start + NextCodec->Len;
		unsigned char *OutEnd	= NextCodec->End;

		if (Codec->StreamData) {
			count = strlen((unsigned char *)(Codec->StreamData));
			processed = 0;
			do {
				if ((count - processed) < BUFSIZE) {
					FlushOutStream(count - processed);
					memcpy(Out, (unsigned char *)(Codec->StreamData) + processed, (count - processed));
					Out += (count - processed);
					NextCodec->Len += (count - processed);
					break;
				}
				FlushOutStream(BUFSIZE - 1);
				memcpy(Out, (unsigned char *)(Codec->StreamData) + processed, BUFSIZE - 1);
				Out += (BUFSIZE - 1);
				NextCodec->Len += (BUFSIZE - 1);
				processed += (BUFSIZE - 1);
			} while (processed < count);
		}

		

		count = Codec->Len;
		do {
			if (count < BUFSIZE) {
				FlushOutStream(count);
				memcpy(Out, In, count);
				Out += count;
				In += count;
				NextCodec->Len += count;
				Len += count;
				break;
			}
			FlushOutStream(BUFSIZE - 1);
			memcpy(Out, In, BUFSIZE - 1);
			Out += (BUFSIZE - 1);
			In += (BUFSIZE - 1);
			NextCodec->Len += (BUFSIZE - 1);
			Len += (BUFSIZE - 1);
			count -= (BUFSIZE - 1);
		} while (count > 0);

		EndFlushStream;
	}
        if (Len >= Codec->Min) {
            return(Len);
        }

        return(Codec->Len);
}


static int
RFC2231_7Bit_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In		= Codec->Start;
	unsigned char	*Out		= NextCodec->Start+NextCodec->Len;
	unsigned long	Len		= 0;
	unsigned char	*OutEnd	= NextCodec->End;

	while (Len<Codec->Len) {
		FlushOutStream(6);

		if (In[0] == '%') {
			if (Codec->Len > (Len + 1)) {
				Out[0] = LeftHexChar[In[1]] + RightHexChar[In[2]];
				Out++;
				In += 3;
				NextCodec->Len++;
				Len += 3;
				continue;
			} else if (!Codec->EOS) {
			    if (Len >= Codec->Min) {
				return(Len);
			    }

			    return(Codec->Len);
			}
		}

		Out[0] = In[0];
		Out++;
		In++;
		NextCodec->Len++;
		Len++;
	}

	EndFlushStream;

	return(Len);
}

static int
RFC2231_7Bit_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In;
	unsigned char	*Out;
	unsigned long	Len;
	unsigned char	*OutEnd;
	unsigned long	outLen;
	unsigned long	wordLimit;

	In				= Codec->Start;
	Out			= NextCodec->Start+NextCodec->Len;
	Len			= 0;
	OutEnd		= NextCodec->End;

	wordLimit	= (unsigned long)(Codec->StreamData);

	if (wordLimit > 0) {
		outLen = 0;

		while (Len < Codec->Len) {
			FlushOutStream(6);

			if ((In[0] < 0x80) && AttributeChar[In[0]]) {
				outLen++;
				if (outLen < wordLimit) {
					Out[0] = In[0];
					Out++;
					outLen++;
					In++;
					NextCodec->Len++;
					Len++;
					continue;
				}

				break;
			} else {
				outLen += 3;
				if (outLen < wordLimit) {
					Out[0] = '%';
					Out[1] = HexChar[(In[0] & 0xF0) >> 4];
					Out[2] = HexChar[In[0] & 0x0F];
					Out += 3;
					In++;
					NextCodec->Len += 3;
					Len++;
					continue;
				}
				break;
			}
		}
	} else {
		while (Len < Codec->Len) {
			FlushOutStream(6);

			if ((In[0] < 0x80) && AttributeChar[In[0]]) {
				Out[0] = In[0];
				Out++;
				In++;
				NextCodec->Len++;
				Len++;
				continue;
			} else {
				Out[0] = '%';
				Out[1] = HexChar[(In[0] & 0xF0) >> 4];
				Out[2] = HexChar[In[0] & 0x0F];
				Out += 3;
				In++;
				NextCodec->Len += 3;
				Len++;
				continue;
			}
		}
	}

	EndFlushStream;

	return(Len);
}

/* This codec can be used to convert an NMAP MIME command param (like filename) to a utf-8 string */
/* It requires the param to be the only thing in the input stream and that the entire param be there */

static int
RFC2231_NMAP_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned long	Len = 0;

	if (Codec->EOS) {
		unsigned char	*In = Codec->Start;
		unsigned char	*Out = NextCodec->Start + NextCodec->Len;
		unsigned char	*OutEnd = NextCodec->End;
		unsigned char	*endPtr = In + Codec->Len;
		unsigned long	count;
		StreamStruct	*rfc2047Codec;
		StreamStruct	*transportCodec;
		StreamStruct	*charsetCodec;
		unsigned char	*charset;
		unsigned char	*lang;
		unsigned char	*encodedWord;
		unsigned char	*nonEncodedWord;
		unsigned char	*ptr;

		if ((In + 1) < endPtr) {
			if ((*In != '*') || (*(In + 1) != '=')) {
				if ((*In != '=') || (*(In + 1) != '?')) {
					/* no transport encoding but somebody might of put 8 bit chars in a header */
					charset = Codec->Charset;
					if ((charset != NULL) && ((toupper(charset[0]) != 'U') || ((XplStrCaseCmp(charset, "UTF-8") != 0)	&& (XplStrCaseCmp(charset,"US-ASCII") != 0)))) {
						charsetCodec = GetStream(charset, FALSE);
						if (charsetCodec) {
							charsetCodec->Next	= Codec->Next;
							charsetCodec->End		= Codec->Start + Codec->Len;
							charsetCodec->EOS		= TRUE;
							do {
								charsetCodec->Start	= In;
								charsetCodec->Len		= Codec->Len - Len;

								count = charsetCodec->Codec(charsetCodec, charsetCodec->Next);

								Len += count;
								In += count;
							} while (Len < Codec->Len);
							Out = NextCodec->Start + NextCodec->Len;
                                                        ReleaseStream(charsetCodec);
							return(Len);					
						}
					} 

					/* No charset translation needed */
					count = Codec->Len - Len;
					do {
						if (count < BUFSIZE) {
							FlushOutStream(count);
							memcpy(Out, In, count);
							In += count;
							Len += count;
							Out += count;
							NextCodec->Len += count;
							break;
						}
						FlushOutStream(BUFSIZE - 1);
						memcpy(Out, In, BUFSIZE - 1);
						In += (BUFSIZE - 1);
						Len += (BUFSIZE - 1);
						Out += (BUFSIZE - 1);
						NextCodec->Len += (BUFSIZE - 1);
						count -= (BUFSIZE - 1);
					} while (count > 0);
					EndFlushStream;
					return(Len);
				} 

				/* possibly rfc2047 */
				rfc2047Codec = GetStream("RFC1522", FALSE);
				if (rfc2047Codec) {
					rfc2047Codec->Next	= Codec->Next;
					rfc2047Codec->End		= Codec->Start + Codec->Len;
					rfc2047Codec->EOS		= TRUE;

					while (Len < Codec->Len) {
						rfc2047Codec->Start	= In;
						rfc2047Codec->Len		= Codec->Len - Len;

						count = rfc2047Codec->Codec(rfc2047Codec, rfc2047Codec->Next);

						Len += count;
						In += count;
					}
					Out = NextCodec->Start + NextCodec->Len;
                                        ReleaseStream(rfc2047Codec);
					return(Len);					
				}

				/* We did not get the stream */
				/* pass it through as is */
				count = Codec->Len - Len;

				do {
					if (count < BUFSIZE) {
						FlushOutStream(count);
						memcpy(Out, In, count);
						In += count;
						Len += count;
						Out += count;
						NextCodec->Len += count;
						break;
					}
					FlushOutStream(BUFSIZE - 1);
					memcpy(Out, In, BUFSIZE - 1);
					In += (BUFSIZE - 1);
					Len += (BUFSIZE - 1);
					Out += (BUFSIZE - 1);
					NextCodec->Len += (BUFSIZE - 1);
					count -= (BUFSIZE - 1);
				} while (count > 0);
				EndFlushStream;
				return(Len);
			}

			/* possibly rfc2231 */
			encodedWord = NULL;
			charset = In + 2;
			lang = charset + 1;
			while (lang < endPtr) {
				if (*lang != '\'') {
					lang++;
					continue;
				}
				lang++;
				ptr = lang;	
				while (ptr < endPtr) {
					if (*ptr != '\'') {
						ptr++;
						continue;
					}
					encodedWord = ptr + 1;
					break;
				}
				break;
			}

			if (encodedWord) {
				nonEncodedWord = endPtr;
				ptr = encodedWord;

				/* Look for a partial word encoding boundary */
				while (ptr < endPtr) {
					if (*ptr != ';') {
						ptr++;
						continue;
					}
					nonEncodedWord = ptr;
					break;
				}

				/* skip prefix */
				count = encodedWord - In;
				In += count;
				Len += count;

				transportCodec = GetStream("RFC2231_7bit", FALSE);

				if (transportCodec) {
					*(lang - 1) = '\0';
					if ((tolower(charset[0]) == 'u') && ((XplStrCaseCmp(charset, "utf-8") == 0) || (XplStrCaseCmp(charset, "us-ascii") == 0))) {
						/* no charset translation needed */
						*(lang - 1) = '\'';

						transportCodec->End		= nonEncodedWord;
						transportCodec->EOS		= TRUE;
						transportCodec->Next		= Codec->Next;

						while (In < nonEncodedWord) {
							transportCodec->Start	= In;
							transportCodec->Len		= nonEncodedWord - In;

							count = transportCodec->Codec(transportCodec, transportCodec->Next);

							Len += count;
							In += count;
						}
						Out = NextCodec->Start + NextCodec->Len;
						if (Len == Codec->Len) {
							ReleaseStream(transportCodec);
							return(Len);
						}
					} else {
						charsetCodec = GetStream(charset, FALSE);
						*(lang - 1) = '\'';

						if (charsetCodec) {

							transportCodec->End		= nonEncodedWord;
							transportCodec->EOS		= TRUE;
							transportCodec->Next		= charsetCodec;
							charsetCodec->Next		= Codec->Next;

							while (In < nonEncodedWord) {
								transportCodec->Start	= In;
								transportCodec->Len		= nonEncodedWord - In;

								count = transportCodec->Codec(transportCodec, transportCodec->Next);

								Len += count;
								In += count;
							}
							Out = NextCodec->Start + NextCodec->Len;
							ReleaseStream(charsetCodec);
							if (Len == Codec->Len) {
								ReleaseStream(transportCodec);
								return(Len);
							}
						}
					}
					*(lang - 1) = '\'';

					ReleaseStream(transportCodec);
				}

				/* skip partial word encoding boundary */
				if (nonEncodedWord < endPtr) {
					In++;
					Len++;		
				}
			} 
		}
		
		/* flush whatever is left */
		count = Codec->Len - Len;
		if (count > 0) {
			do {
				if (count < BUFSIZE) {
					FlushOutStream(count);
					memcpy(Out, In, count);
					In += count;
					Len += count;
					Out += count;
					NextCodec->Len += count;
					break;
				}
				FlushOutStream(BUFSIZE - 1);
				memcpy(Out, In, BUFSIZE - 1);
				In += (BUFSIZE - 1);
				Len += (BUFSIZE - 1);
				Out += (BUFSIZE - 1);
				NextCodec->Len += (BUFSIZE - 1);
				count -= (BUFSIZE - 1);
			} while (count > 0);
		}
		EndFlushStream;
		return(Len);
	} 

	/* Go get the rest */
        if (Len >= Codec->Min) {
            return(Len);
        }
	/* Could not fit it all in the buffer; toss it */
        return(Codec->Len);
}

/* This codec can be used to convert text string to an NMAP MIME command param (like filename) */
/* If Codec->Charset is NULL, UTF-8 is assumed */
/* It requires the entire string to be the input stream (needs EOS) */

static int
RFC2231_NMAP_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*Out;								/* current pointer in output stream */
	unsigned char	*OutEnd;							/* pointer to the end of the output buffer */
	unsigned char	*In;								/* current pointer in input stream */
	unsigned char	*InEnd;							/* pointer to the end of the input stream data */
	unsigned long	Len;								/* number of bytes processed in the input stream */

	unsigned char	*ptr;								/* general purpose char ptr */
	unsigned long	count;							/* general purpose counter */

	StreamStruct	*transportCodec;				/* next codec for 8bit values (7bit encodes values)*/
	unsigned long	charsetLen;						/* number of bytes in the name of the stream charset */
	BOOL				needTransportEncoding;		/* the presence of 8 bit chars force this to true */ 

	Len = 0;

	if (Codec->EOS) {
		/* Initialize stream variables */
		In = Codec->Start;
		Out = NextCodec->Start + NextCodec->Len;
		OutEnd = NextCodec->End;

		/* determine if tranport encoding is needed */
		/* count the characters that don't need encoded */
		ptr = Codec->Start;
		InEnd = Codec->Start + Codec->Len;
		needTransportEncoding = FALSE;
		while (ptr < InEnd) {
			if (quotableText[*ptr]) {
				ptr++;
				continue;
			}

			needTransportEncoding = TRUE;
			break;
		}

		if ((!needTransportEncoding) || ((transportCodec = GetStream("RFC2231_7bit", TRUE)) == NULL)) {
			/* Pass it through */
			count = Codec->Len;
			do {
				if (count < BUFSIZE) {
					FlushOutStream(count);
					memcpy(Out, In, count);
					In += count;
					Len += count;
					Out += count;
					NextCodec->Len += count;
					break;
				}
				FlushOutStream(BUFSIZE - 1);
				memcpy(Out, In, BUFSIZE - 1);
				In += (BUFSIZE - 1);
				Len += (BUFSIZE - 1);
				Out += (BUFSIZE - 1);
				NextCodec->Len += (BUFSIZE - 1);
				count -= (BUFSIZE - 1);
			} while (count > 0);

			EndFlushStream;
			return(Len);
		} 

		/* Create 2231 NMAP value */
		/* send prefix */
		if (!Codec->Charset) {
			FlushOutStream(9);  /* *=utf-8'' */

			/* send rfc2231 encoding char */
			Out[0] = '*';
			Out++;
			NextCodec->Len++;

			/* Send equal sign */
			Out[0] = '=';
			Out++;
			NextCodec->Len++;

			/* send charset */
			memcpy(Out, "UTF-8", 5);
			Out += 5;
			NextCodec->Len += 5;
		} else {
			charsetLen = strlen(Codec->Charset);

			FlushOutStream(4 + charsetLen);  /* *=charset'' */

			/* send rfc2231 encoding char */
			Out[0] = '*';
			Out++;
			NextCodec->Len++;

			/* Send equal sign */
			Out[0] = '=';
			Out++;
			NextCodec->Len++;

			/* send charset */
			memcpy(Out, (unsigned char *)(Codec->Charset), charsetLen);
			Out += charsetLen;
			NextCodec->Len += charsetLen;
		}

		/* add lang delimiters */
		Out[0] = '\'';
		Out++;
		NextCodec->Len++;

		Out[0] = '\'';
		Out++;
		NextCodec->Len++;

		transportCodec->End	= Codec->End;
		transportCodec->EOS	= TRUE;
		transportCodec->Next	= Codec->Next;
		transportCodec->StreamData	= (void *)0;  /* unlimited word length */
		do {
			transportCodec->Start = In;
			transportCodec->Len	= Codec->Len;

			count = transportCodec->Codec(transportCodec, transportCodec->Next);
			In += count;
			Len += count;
		} while (Len < Codec->Len);
		ReleaseStream(transportCodec);
		return(Len);
	}

	/* Go get the rest */
        if (Len >= Codec->Min) {
            return(Len);
        }

	/* it won't all fit in the buffer, toss it */
        return(Codec->Len);
}

/* RFC2231_Value_Encode encodes a string into an RFC2231 parameter value */
/* Codec->StreamData must be a pointer to the name of the parameter */
/* The entire stream must be available */
/* The charset of the stream must be given */

static int
RFC2231_Value_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*Out;								/* current pointer in output stream */
	unsigned char	*OutEnd;							/* pointer to the end of the output buffer */
	unsigned char	*In;								/* current pointer in input stream */
	unsigned char	*InEnd;							/* pointer to the end of the input stream data */
	unsigned long	Len;								/* number of bytes processed in the input stream */

	unsigned char	*ptr;								/* general purpose char ptr */
	unsigned long	count;							/* general purpose counter */

	StreamStruct	*quotedCodec;					/* next codec for 7bit values (creates quoted string) */
	StreamStruct	*transportCodec;				/* next codec for 8bit values (7bit encodes values)*/
	unsigned long	charsetLen;						/* number of bytes in the name of the stream charset */
	unsigned long	nameLen;							/* number of bytes in the parameter name */
	unsigned long	attrChars;						/* number of attribute-chars as defined by rfc2231 */
	BOOL				needTransportEncoding;		/* the presence of 8 bit chars force this to true */ 

	unsigned long	bytesInWord;					/* number of bytes of value string to be sent on this line */					

	unsigned long	lineCount;						/* number of lines already created */

	Len = 0;

	if (Codec->EOS) {
		/* find the length of the parameter name */
		nameLen = strlen((unsigned char *)(Codec->StreamData));

		/* Initialize stream variables */
		In = Codec->Start;
		Out = NextCodec->Start + NextCodec->Len;
		OutEnd = NextCodec->End;

		/* determine if tranport encoding is needed */
		/* count the characters that don't need encoded */
		ptr = Codec->Start;
		InEnd = Codec->Start + Codec->Len;
		attrChars = 0;
		needTransportEncoding = FALSE;
		while (ptr < InEnd) {
			if (AttributeChar[*ptr]) {
				attrChars++;
				ptr++;
				continue;
			}

			if (*ptr < 0x0080) {
				ptr++;
				continue;
			}

			needTransportEncoding = TRUE;
			
			ptr++;

			while (ptr < InEnd) {
				if (AttributeChar[*ptr]) {
					attrChars++;
				}
				ptr++;
			}
		}

		if (!needTransportEncoding) {
			/* Use quoted string */
			quotedCodec = GetStream("quoted-string", TRUE);

			if (quotedCodec) {
				quotedCodec->EOS = TRUE;

				/* single line quoted string */
				if (Codec->Len < RFC2822_LONG_LINE) {
					FlushOutStream(nameLen + 1);
					memcpy(Out, (unsigned char *)(Codec->StreamData), nameLen);
					Out += nameLen;
					NextCodec->Len += nameLen;

					*Out = '=';
					Out++;
					NextCodec->Len++;

					do {
						quotedCodec->Start = Codec->Start + Len;
						quotedCodec->Len = Codec->Len - Len;
						quotedCodec->StreamData	= (void *)(0);

						count = quotedCodec->Codec(quotedCodec, Codec->Next);
						In += count;
						Len += count;
					} while (Len < Codec->Len);

					ReleaseStream(quotedCodec);
					return(Len);
				}

				/* multi-line quoted string */
				lineCount = 0;
				
				do {
					if (lineCount > 0) {
						FlushOutStream(nameLen + 8);  /* ;\r\n\tname*99=  */
						memcpy(Out, ";\r\n\t", 4);
						Out += 4;
						NextCodec->Len += 4;
					} else {
						FlushOutStream(nameLen + 3);  /* name*0=  */
					}

					/* send name */
					memcpy(Out, (unsigned char *)(Codec->StreamData), nameLen);
					Out += nameLen;
					NextCodec->Len += nameLen;

					if (lineCount < 10) {
						Out[0] = '*';
						Out[1] = '0' + (unsigned char)lineCount;
						Out[2] = '=';
						Out += 3;
						NextCodec->Len += 3;
						quotedCodec->StreamData	= (void *)(RFC2822_LONG_LINE - (nameLen + 3));
					} else if (lineCount < 100) {
						Out[0] = '*';
						Out[1] = '0' + ((unsigned char)lineCount / 10);
						Out[2] = '0' + ((unsigned char)lineCount % 10);
						Out[3] = '=';
						Out += 4;
						NextCodec->Len += 4;
						quotedCodec->StreamData	= (void *)(RFC2822_LONG_LINE - (nameLen + 4));
					} else {
						/* This codec will not produce more than 100 lines */
						break;
					}

					quotedCodec->Start = In;
					quotedCodec->Len = Codec->Len - Len;

					count = quotedCodec->Codec(quotedCodec, Codec->Next);

					In += count;
					Len += count;

					Out = NextCodec->Start + NextCodec->Len;

					lineCount++;
				} while (Len < Codec->Len);

				ReleaseStream(quotedCodec);
				return(Len);
			}
			/* Failed to get quoted codec */   
			goto ErrorPath; 
		} 

		transportCodec = GetStream("RFC2231_7bit", TRUE);
		if (transportCodec) {

			charsetLen = strlen(Codec->Charset);

			count = nameLen + 2 + charsetLen + 2 + attrChars + ((Codec->Len - attrChars) * 3);		/* name*=charset''(attribute chars + (ext-octet chars * 3)) */ 
			/* single line encoded */
			if (count < RFC2822_LONG_LINE) {
				/* send prefix */
				FlushOutStream(4 + nameLen + charsetLen);  /* asterisk, equal, lang dilimiters */
				/* send name */
				memcpy(Out, (unsigned char *)(Codec->StreamData), nameLen);
				Out += nameLen;
				NextCodec->Len += nameLen;

				/* add rfc2231 encoding char */
				Out[0] = '*';
				Out++;
				NextCodec->Len++;

				/* Send equal sign */
				Out[0] = '=';
				Out++;
				NextCodec->Len++;

				/* send charset */
				memcpy(Out, (unsigned char *)(Codec->Charset), charsetLen);
				Out += charsetLen;
				NextCodec->Len += charsetLen;

				/* add lang delimiters */
				Out[0] = '\'';
				Out++;
				NextCodec->Len++;

				Out[0] = '\'';
				Out++;
				NextCodec->Len++;

				transportCodec->End	= Codec->End;
				transportCodec->EOS	= TRUE;
				transportCodec->Next	= Codec->Next;
				transportCodec->StreamData	= (void *)0;
				do {
					transportCodec->Start = In;
					transportCodec->Len	= Codec->Len;

					count = transportCodec->Codec(transportCodec, transportCodec->Next);
					In += count;
					Len += count;
				} while (Len < Codec->Len);
				ReleaseStream(transportCodec);
				return(Len);
			}

			/* multi-line encoded string */
			FlushOutStream(nameLen + charsetLen + 6);  /* name*0*=charset''  */

			memcpy(Out, (unsigned char *)(Codec->StreamData), nameLen);
			Out += nameLen;
			NextCodec->Len += nameLen;

			memcpy(Out, "*0*=", 4);
			Out += 4;
			NextCodec->Len += 4;

			memcpy(Out, (unsigned char *)(Codec->Charset), charsetLen);
			Out += charsetLen;
			NextCodec->Len += charsetLen;

			Out[0] = '\'';
			Out++;
			NextCodec->Len++;

			Out[0] = '\'';
			Out++;
			NextCodec->Len++;

			transportCodec->End			= Codec->End;
			transportCodec->EOS			= TRUE;
			transportCodec->Next			= Codec->Next;

			transportCodec->Start		= In;
			transportCodec->Len			= Codec->Len;
			transportCodec->StreamData	= (void *)(RFC2822_LONG_LINE - (nameLen + 4 + charsetLen + 2));

			count = transportCodec->Codec(transportCodec, transportCodec->Next);
			Len += count;
			In += count;
			Out = NextCodec->Start + NextCodec->Len;

			lineCount = 1;

			do {
				FlushOutStream(nameLen + 8);  /* ;\r\n\tname*99=  */
				memcpy(Out, ";\r\n\t", 4);
				Out += 4;
				NextCodec->Len += 4;

				/* send name */
				memcpy(Out, (unsigned char *)(Codec->StreamData), nameLen);
				Out += nameLen;
				NextCodec->Len += nameLen;

				if (lineCount < 10) {
					Out[0] = '*';
					Out[1] = '0' + (unsigned char)lineCount;
					Out[2] = '*';
					Out[3] = '=';
					Out += 4;
					NextCodec->Len += 4;
					transportCodec->StreamData	= (void *)(RFC2822_LONG_LINE - (nameLen + 4));

				} else if (lineCount < 100) {
					Out[0] = '*';
					Out[1] = '0' + ((unsigned char)lineCount / 10);
					Out[2] = '0' + ((unsigned char)lineCount % 10);
					Out[3] = '*';
					Out[4] = '=';
					Out += 5;
					NextCodec->Len += 5;
					transportCodec->StreamData	= (void *)(RFC2822_LONG_LINE - (nameLen + 5));
				} else {
					/* This codec will not produce more than 100 lines */
					break;
				}

				transportCodec->Start = In;
				transportCodec->Len = Codec->Len - Len;

				count = transportCodec->Codec(transportCodec, Codec->Next);
				In += count;
				Len += count;
				Out = NextCodec->Start + NextCodec->Len;

				lineCount++;
			} while (Len < Codec->Len);

			ReleaseStream(transportCodec);
			return(Len);
		}

ErrorPath:
		/* send parameter name */
		FlushOutStream(nameLen + 1);
		memcpy(Out, (unsigned char *)(Codec->StreamData), nameLen);
		Out += nameLen;
		NextCodec->Len += nameLen;

		*Out = '=';
		Out++;
		NextCodec->Len++;
		bytesInWord = Codec->Len;

		FlushOutStream(2);
		*Out = '"';
		Out++;
		NextCodec->Len++;

		count = Codec->Len;
		do {
			if (count < BUFSIZE) {
				FlushOutStream(count);
				memcpy(Out, In, count);
				Out += count;
				NextCodec->Len += count;
				In += count;
				Len += count;
				break;
			}
			FlushOutStream(BUFSIZE - 1);
			memcpy(Out, In, BUFSIZE - 1);
			Out += (BUFSIZE - 1);
			NextCodec->Len += (BUFSIZE - 1);
			In += (BUFSIZE - 1);
			Len += (BUFSIZE - 1);
			count -= (BUFSIZE - 1);
		} while (count > 0);

		*Out = '"';
		Out++;
		NextCodec->Len++;
		EndFlushStream;

		return(Len);
	}

	/* Go get the rest */
        if (Len >= Codec->Min) {
            return(Len);
        }

        return(Codec->Len);
}
