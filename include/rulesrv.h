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

/* Format of a rule is as follows:

	<UniqueID> <Active> <Condition> <Len Arg1> <Arg1> <Len Arg2> <Arg2> <Action> <Len Arg1> <Arg1> <Len Arg2> <Arg2> <Bool>

   <UniqueID>					= 8 byte Digits w/leading zeros
   <Active>						= 1 byte ASCII char
	<Condition>					= 1 byte ASCII char
	<Action>						= 1 byte ASCII char
	<Bool>						= 1 byte ASCII char
	<Len Arg1>, <Len Arg2>	= 3 digits, leading zero
	<Arg1>, <Arg2>				= Variable length string, 'Z' (ASCII 90) terminator, length does not include terminator, ALWAYS Terminated, even if empty

	Examples: 
	- Move *ANY* message into mailbox suck							:	xxxxxxxxAA000Z000ZE004suckZ000Z
	- Copy "admin" on message if from line contains "lynn"	:	xxxxxxxxAB004lynnZ000ZD005adminZ000Z
	- move into mailbox suck if from lynn AND subject bummer	:	xxxxxxxxAB004lynnZ000ZXD006bummerZ000ZE004suckZ000Z
	- Copy "admin" and "peter" on message if from line contains "lynn"	:	xxxxxxxxAB004lynnZ000ZD005adminZ000ZD005peterZ000Z
	- Copy "admin" and "peter" on message if from line contains "lynn" and then stop processing and later rules	:	xxxxxxxxAB004lynnZ000ZD005adminZ000ZD005peterZ000ZF000Z000Z

*/

#define	RULE_ACTIVE						'A'								/* No args */
#define	RULE_INACTIVE					'B'								/* No args */

#define	RULE_COND_ANY					'A'								/* No args */
#define	RULE_COND_FROM					'B'								/* Arg1: search string */
#define	RULE_COND_TO					'C'								/* Arg1: search string */
#define	RULE_COND_SUBJECT				'D'								/* Arg1: search string */
#define	RULE_COND_PRIORITY			'E'								/* Arg1: search string */
#define	RULE_COND_HEADER				'F'								/* Arg1: Header field, Arg2: search string */
#define	RULE_COND_BODY					'G'								/* Arg1: search string */
#define	RULE_COND_FROM_NOT			'H'								/* Arg1: search string */
#define	RULE_COND_TO_NOT				'I'								/* Arg1: search string */
#define	RULE_COND_SUBJECT_NOT		'J'								/* Arg1: search string */
#define	RULE_COND_PRIORITY_NOT		'K'								/* Arg1: search string */
#define	RULE_COND_HEADER_NOT			'L'								/* Arg1: Header field, Arg2: search string */
#define	RULE_COND_BODY_NOT			'M'								/* Arg1: search string */
#define	RULE_COND_SIZE_MORE			'N'								/* Arg1: Size in kilobytes */
#define	RULE_COND_SIZE_LESS			'O'								/* Arg1: Size in kilobytes */

#define	RULE_COND_TIMEOFDAYLESS		'P'								/* Arg1: Time of day in seconds */
#define	RULE_COND_TIMEOFDAYMORE		'Q'								/* Arg1: Time of day in seconds */
#define	RULE_COND_WEEKDAYIS			'R'								/* Arg1: Number of week Day.  (0 = Sunday) */
#define	RULE_COND_MONTHDAYIS			'S'								/* Arg1: Day of month */
#define	RULE_COND_MONTHDAYISNOT		'T'								/* Arg1: Day of month */
#define	RULE_COND_MONTHDAYLESS		'U'								/* Arg1: Day of month */
#define	RULE_COND_MONTHDAYMORE		'a'								/* Arg1: Day of month */
#define	RULE_COND_MONTHIS				'b'								/* Arg1: Month */
#define	RULE_COND_MONTHISNOT			'c'								/* Arg1: Month */

#define	RULE_COND_FREE					'd'								/* No args */
#define	RULE_COND_FREENOT				'e'								/* No args */

#define	RULE_COND_HASMIMETYPE		'f'								/* Arg1: Mime type to look for */
#define	RULE_COND_HASMIMETYPENOT	'g'								/* Arg1: Mime type to look for */

#define	RULE_ACT_REPLY					'A'								/* Arg1 : Filename */
#define	RULE_ACT_DELETE				'B'								/* No args */
#define	RULE_ACT_FORWARD				'C'								/* Arg1 : Remote address */
#define	RULE_ACT_COPY					'D'								/* Arg1 : Remote address */
#define	RULE_ACT_MOVE					'E'								/* Arg1 : Mailbox name */
#define	RULE_ACT_STOP					'F'								/* No args */

#define	RULE_ACT_ACCEPT				'G'								/* No args */
#define	RULE_ACT_DECLINE				'H'								/* No args */
#define	RULE_ACT_BOUNCE				'I'								/* No args */

#define	RULE_ACT_BOOL_START			'V'								/* Internal */
#define	RULE_ACT_BOOL_NONE			'W'								/* Internal */
#define	RULE_ACT_BOOL_AND				'X'
#define	RULE_ACT_BOOL_OR				'Y'
#define	RULE_ACT_BOOL_NOT				'Z'
