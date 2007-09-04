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
 * (C) 2007 Patrick Felt
 ****************************************************************************/
#include <config.h>
#include <xpl.h>
#include <msgapi.h>

#define MSGADDR_C

#include <msgaddr.h>

EXPORT const unsigned char MsgAddressCharacters[] = {
    /*    0x00    000    NUL    Null                        */    0, 
    /*    0x01    001    SOH    Start of Heading            */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x02    002    STX    Start Text                  */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x03    003    ETX    End Text                    */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x04    004    EOT    End of Transmisison         */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x05    005    ENQ    Enquiry                     */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x06    006    ACK    Acknowledge                 */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x07    007    BEL    Bell                        */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x08    008    BS        Backspace                */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x09    009    HT        Horizontal Tab           */    MSGADDR_TEXT, 
    /*    0x0A    010    LF        Linefeed                 */    0, 
    /*    0x0B    011    VT        Vertical Tab             */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x0C    012    FF        Formfeed                 */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x0D    013    CR        Carriage Return          */    0, 
    /*    0x0E    014    SO        Shift Out                */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x0F    015    SI        Shift In                 */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x10    016    DLE    Data Link Escape            */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x11    017    DC1    Device Control 1            */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x12    018    DC2    Device Control 2            */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x13    019    DC3    Device Control 3            */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x14    020    DC4    Device Control 4            */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x15    021    NAK    Negative Acknowledgement    */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x16    022    SYN    Synchronous Idle            */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x17    023    ETB    End Transmission Block      */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x18    024    CAN    Cancel                      */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x19    025    EM        End Medium               */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x1A    026    EOF    End of File                 */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x1B    027    ESC    Escape                      */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x1C    028    FS        File Separator           */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x1D    029    GS        Group Separator          */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x1E    030    HOM    Home                        */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x1F    031    NEW    Newline                     */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x20    032    SPA    Space                       */    MSGADDR_TEXT, 
    /*    0x21    033    !        Exclamation Point         */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x22    034    "        Double Quote              */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x23    035    #        Pound Sign                */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x24    036    $        Dollar Sign               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x25    037    %        Percent                   */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x26    038    &        Ampersand                 */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x27    039    '        Apostrophe                */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x28    040    (        Right Parenthesis         */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR, 
    /*    0x29    041    )        Left Parenthesis          */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR, 
    /*    0x2A    042    *        Asterick                  */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x2B    043    +        Plus                      */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x2C    044    ,        Comma                     */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x2D    045    -        Minus                     */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x2E    046    .        Period                    */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x2F    047    /        Forward Slash             */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x30    048    0        Zero                      */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT | MSGADDR_HEXCHAR, 
    /*    0x31    049    1        One                       */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT | MSGADDR_HEXCHAR, 
    /*    0x32    050    2        Two                       */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT | MSGADDR_HEXCHAR, 
    /*    0x33    051    3        Three                     */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT | MSGADDR_HEXCHAR, 
    /*    0x34    052    4        Four                      */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT | MSGADDR_HEXCHAR, 
    /*    0x35    053    5        Five                      */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT | MSGADDR_HEXCHAR, 
    /*    0x36    054    6        Six                       */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT | MSGADDR_HEXCHAR, 
    /*    0x37    055    7        Seven                     */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT | MSGADDR_HEXCHAR, 
    /*    0x38    056    8        Eight                     */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT | MSGADDR_HEXCHAR, 
    /*    0x39    057    9        Nine                      */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT | MSGADDR_HEXCHAR, 
    /*    0x3A    058    :        Colon                     */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x3B    059    ;        Semi-Colon                */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x3C    060    <        Less-than                 */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x3D    061    =        Equal                     */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x3E    062    >        Greater-than              */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x3F    063    ?        Question Mark             */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x40    064    @        At                        */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x41    065    A        Uppercase A               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT | MSGADDR_HEXCHAR, 
    /*    0x42    066    B        Uppercase B               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT | MSGADDR_HEXCHAR, 
    /*    0x43    067    C        Uppercase C               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT | MSGADDR_HEXCHAR, 
    /*    0x44    068    D        Uppercase D               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT | MSGADDR_HEXCHAR, 
    /*    0x45    069    E        Uppercase E               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT | MSGADDR_HEXCHAR, 
    /*    0x46    070    F        Uppercase F               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT | MSGADDR_HEXCHAR, 
    /*    0x47    071    G        Uppercase G               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x48    072    H        Uppercase H               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x49    073    I        Uppercase I               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x4A    074    J        Uppercase J               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x4B    075    K        Uppercase K               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x4C    076    L        Uppercase L               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x4D    077    M        Uppercase M               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x4E    078    N        Uppercase N               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x4F    079    O        Uppercase O               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x50    080    P        Uppercase P               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x51    081    Q        Uppercase Q               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x52    082    R        Uppercase R               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x53    083    S        Uppercase S               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x54    084    T        Uppercase T               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x55    085    U        Uppercase U               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x56    086    V        Uppercase V               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x57    087    W        Uppercase W               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x58    088    X        Uppercase X               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x59    089    Y        Uppercase Y               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x5A    090    Z        Uppercase Z               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x5B    091    [                                  */    MSGADDR_TEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x5C    092    \        Back Slash                */    MSGADDR_TEXT | MSGADDR_XCHAR, 
    /*    0x5D    093    ]                                  */    MSGADDR_TEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x5E    094    ^                                  */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x5F    095    _        Underscore                */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x60    096    `                                  */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x61    097    a        Lowercase a               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x62    098    b        Lowercase b               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x63    099    c        Lowercase c               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x64    100    d        Lowercase d               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x65    101    e        Lowercase e               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x66    102    f        Lowercase f               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x67    103    g        Lowercase g               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x68    104    h        Lowercase h               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x69    105    i        Lowercase i               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x6A    106    j        Lowercase j               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x6B    107    k        Lowercase k               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x6C    108    l        Lowercase l               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x6D    109    m        Lowercase m               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x6E    110    n        Lowercase n               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x6F    111    o        Lowercase o               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x70    112    p        Lowercase p               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x71    113    q        Lowercase q               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x72    114    r        Lowercase r               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x73    115    s        Lowercase s               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x74    116    t        Lowercase t               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x75    117    u        Lowercase u               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x76    118    v        Lowercase v               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x77    119    w        Lowercase w               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x78    120    x        Lowercase x               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x79    121    y        Lowercase y               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x7A    122    z        Lowercase z               */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x7B    123    {                                  */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x7C    124    |                                  */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x7D    125    }                                  */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x7E    126    ~                                  */    MSGADDR_TEXT | MSGADDR_ATEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_XCHAR | MSGADDR_CTEXT, 
    /*    0x7F    127    DEL                                */    MSGADDR_TEXT | MSGADDR_DTEXT | MSGADDR_QTEXT | MSGADDR_CTEXT, 
    /*    0x80    128                                       */    0, 
    /*    0x81    129                                       */    0, 
    /*    0x82    130                                       */    0, 
    /*    0x83    131                                       */    0, 
    /*    0x84    132                                       */    0, 
    /*    0x85    133                                       */    0, 
    /*    0x86    134                                       */    0, 
    /*    0x87    135                                       */    0, 
    /*    0x88    136                                       */    0, 
    /*    0x89    137                                       */    0, 
    /*    0x8A    138                                       */    0, 
    /*    0x8B    139                                       */    0, 
    /*    0x8C    140                                       */    0, 
    /*    0x8D    141                                       */    0, 
    /*    0x8E    142                                       */    0, 
    /*    0x8F    143                                       */    0, 
    /*    0x90    144                                       */    0, 
    /*    0x91    145                                       */    0, 
    /*    0x92    146                                       */    0, 
    /*    0x93    147                                       */    0, 
    /*    0x94    148                                       */    0, 
    /*    0x95    149                                       */    0, 
    /*    0x96    150                                       */    0, 
    /*    0x97    151                                       */    0, 
    /*    0x98    152                                       */    0, 
    /*    0x99    153                                       */    0, 
    /*    0x9A    154                                       */    0, 
    /*    0x9B    155                                       */    0, 
    /*    0x9C    156                                       */    0, 
    /*    0x9D    157                                       */    0, 
    /*    0x9E    158                                       */    0, 
    /*    0x9F    159                                       */    0, 
    /*    0xA0    160                                       */    0, 
    /*    0xA1    161                                       */    0, 
    /*    0xA2    162                                       */    0, 
    /*    0xA3    163                                       */    0, 
    /*    0xA4    164                                       */    0, 
    /*    0xA5    165                                       */    0, 
    /*    0xA6    166                                       */    0, 
    /*    0xA7    167                                       */    0, 
    /*    0xA8    168                                       */    0, 
    /*    0xA9    169                                       */    0, 
    /*    0xAA    170                                       */    0, 
    /*    0xAB    171                                       */    0, 
    /*    0xAC    172                                       */    0, 
    /*    0xAD    173                                       */    0, 
    /*    0xAE    174                                       */    0, 
    /*    0xAF    175                                       */    0, 
    /*    0xB0    176                                       */    0, 
    /*    0xB1    177                                       */    0, 
    /*    0xB2    178                                       */    0, 
    /*    0xB3    179                                       */    0, 
    /*    0xB4    180                                       */    0, 
    /*    0xB5    181                                       */    0, 
    /*    0xB6    182                                       */    0, 
    /*    0xB7    183                                       */    0, 
    /*    0xB8    184                                       */    0, 
    /*    0xB9    185                                       */    0, 
    /*    0xBA    186                                       */    0, 
    /*    0xBB    187                                       */    0, 
    /*    0xBC    188                                       */    0, 
    /*    0xBD    189                                       */    0, 
    /*    0xBE    190                                       */    0, 
    /*    0xBF    191                                       */    0, 
    /*    0xC0    192                                       */    0, 
    /*    0xC1    193                                       */    0, 
    /*    0xC2    194                                       */    0, 
    /*    0xC3    195                                       */    0, 
    /*    0xC4    196                                       */    0, 
    /*    0xC5    197                                       */    0, 
    /*    0xC6    198                                       */    0, 
    /*    0xC7    199                                       */    0, 
    /*    0xC8    200                                       */    0, 
    /*    0xC9    201                                       */    0, 
    /*    0xCA    202                                       */    0, 
    /*    0xCB    203                                       */    0, 
    /*    0xCC    204                                       */    0, 
    /*    0xCD    205                                       */    0, 
    /*    0xCE    206                                       */    0, 
    /*    0xCF    207                                       */    0, 
    /*    0xD0    208                                       */    0, 
    /*    0xD1    209                                       */    0, 
    /*    0xD2    210                                       */    0, 
    /*    0xD3    211                                       */    0, 
    /*    0xD4    212                                       */    0, 
    /*    0xD5    213                                       */    0, 
    /*    0xD6    214                                       */    0, 
    /*    0xD7    215                                       */    0, 
    /*    0xD8    216                                       */    0, 
    /*    0xD9    217                                       */    0, 
    /*    0xDA    218                                       */    0, 
    /*    0xDB    219                                       */    0, 
    /*    0xDC    220                                       */    0, 
    /*    0xDD    221                                       */    0, 
    /*    0xDE    222                                       */    0, 
    /*    0xDF    223                                       */    0, 
    /*    0xE0    224                                       */    0, 
    /*    0xE1    225                                       */    0, 
    /*    0xE2    226                                       */    0, 
    /*    0xE3    227                                       */    0, 
    /*    0xE4    228                                       */    0, 
    /*    0xE5    229                                       */    0, 
    /*    0xE6    230                                       */    0, 
    /*    0xE7    231                                       */    0, 
    /*    0xE8    232                                       */    0, 
    /*    0xE9    233                                       */    0, 
    /*    0xEA    234                                       */    0, 
    /*    0xEB    235                                       */    0, 
    /*    0xEC    236                                       */    0, 
    /*    0xED    237                                       */    0, 
    /*    0xEE    238                                       */    0, 
    /*    0xEF    239                                       */    0, 
    /*    0xF0    240                                       */    0, 
    /*    0xF1    241                                       */    0, 
    /*    0xF2    242                                       */    0, 
    /*    0xF3    243                                       */    0, 
    /*    0xF4    244                                       */    0, 
    /*    0xF5    245                                       */    0, 
    /*    0xF6    246                                       */    0, 
    /*    0xF7    247                                       */    0, 
    /*    0xF8    248                                       */    0, 
    /*    0xF9    249                                       */    0, 
    /*    0xFA    250                                       */    0, 
    /*    0xFB    251                                       */    0, 
    /*    0xFC    252                                       */    0, 
    /*    0xFD    253                                       */    0, 
    /*    0xFE    254                                       */    0, 
    /*    0xFF    255                                       */    0
};


/* RFC 3461 - 4. Additional parameters for RCPT and MAIL commands
 *     xtext = *( xchar / hexchar )
 *
 *     xchar = any ASCII CHAR between "!" (33) and "~" (126) inclusive,
 *             except for "+" and "=".
 *
 *     ; "hexchar"s are intended to encode octets that cannot appear
 *     ; as ASCII characters within an esmtp-value.
 *
 *     hexchar = ASCII "+" immediately followed by two upper case
 *              hexadecimal digits
 *
 *  When encoding an octet sequence as xtext:
 *
 *  +  Any ASCII CHAR between "!" and "~" inclusive, except for "+" and
 *     "=", MAY be encoded as itself.  (A CHAR in this range MAY instead
 *     be encoded as a "hexchar", at the implementor's discretion.)
 *
 *  +  ASCII CHARs that fall outside the range above must be encoded as
 *     "hexchar". */
EXPORT BOOL
MsgIsXText(unsigned char *in, unsigned char *limit, unsigned char **out)
{
    unsigned char *ptr;

    ptr = in;

    while (ptr < limit) {
        if (MSG_IS_XCHAR(*ptr)) {
            /* Specification: xchar */
            ptr++;
            continue;
        } else if ((ptr[0] == '+') && MSG_IS_HEXCHAR(ptr[1]) && MSG_IS_HEXCHAR(ptr[2])) {
            /* Specification:    hexchar */
            ptr += 3;
            continue;
        } else if ((ptr > in) && ((ptr[0] == ' ') || (ptr[0] == '\r') || (ptr[0] == '\t') || (ptr[0] == '\0') || (ptr[0] == '\n'))) {
            if (out) {
                *out = ptr;
            }
            return(TRUE);
        }

        break;
    }

    if (out) {
        *out = in;
    }
    return(FALSE);
}


/*  RFC 2822 - 3.2.3 Folding white space and comments
 *       FWS = ([*WSP CRLF] 1*WSP) / obs-FWS
 *       CFWS = *([FWS] comment) (([FWS] comment) / FWS)
 *
 *      comment = "(" *([FWS] ccontent) [FWS] ")"
 *      ccontent = ctext / quoted-pair / comment
 *   
 *  Returns non-zero if 'ptr' is an RFC2822 comment */
EXPORT int 
MsgIsComment(unsigned char *base, unsigned char *limit, unsigned char **out)
{
    unsigned char *ptr = base;

    /* FWS */
    if (ptr) {
        if (MSG_IS_WSP(ptr[0])) {
            if (MSG_IS_CRLF(++ptr)) {
                ptr += 2;

                if (MSG_IS_WSP(ptr[0])) {
                    ptr++;
                } else {
                    if (out) {
                        *out = ++ptr;
                    }

                    return(FALSE);
                }
            }
        }

        if (ptr[0] == MSGADDR_COMMENT_START) {
            while (++ptr < limit) {
                if (MSG_IS_CTEXT(ptr[0])) {
                    continue;
                } else if ((ptr[0] == '\\') && (MSG_IS_TEXT(ptr[1]))) {
                    ptr++;
                    continue;
                } else if (ptr[0] == MSGADDR_COMMENT_END) {
                    if (out) {
                        *out = ++ptr;
                    }

                    return(TRUE);
                } else if ((ptr[0] == MSGADDR_COMMENT_START) && (MsgIsComment(ptr, limit, &ptr))) {
                    continue;
                }
            }
        }
    }

    if (out) {
        *out = ptr;
    }

    return(FALSE);
}

EXPORT BOOL 
MsgParseAddress(unsigned char *addressLine, size_t addressLineLength, unsigned char **local_part, unsigned char **domain ) {
    /*TODO: this is a terribly simple parser of an email address into the local_part and the domain.  this needs to be better */
    unsigned char *at = strchr(addressLine, '@');
    if (at) {
        *at = '\0';
        (*local_part)= MemStrdup(addressLine);
        (*domain) = MemStrdup(at+1);
        *at = '@';
        return TRUE;
    }
    return FALSE;
}
#if 0
TODO: this is going to get replaced later with something better!!
/*  RFC 2822 - 3.4.1 Addr-spec specification
 *      addr-spec = local-part"@"domain
 *      local-part = dot-atom / quoted-string / obs-local-part
 *
 *      dot-atom = [CFWS]dot-atom-text[CFWS]
 *      dot-atom-text = 1*atext*("."1*atext)
 *
 *      quoted-string = [CFWS]DQUOTE*([FWS]qcontent)[FWS]DQUOTE[CFWS]
 *      qcontent = qtext / quoted-pair
 *
 *      quoted-pair = ("\"text) / obs-qp
 *
 *      FWS = ([*WSP CRLF] 1*WSP) / obs-FWS
 *      CFWS = *([FWS] comment) (([FWS] comment) / FWS)  */
EXPORT BOOL 
MsgParseAddress(unsigned char *addressLine, size_t addressLineLength, unsigned char *delimiters, unsigned char **local_part, unsigned char **domain )
{
    unsigned char *ptr;
    unsigned char *ptrLimit;
    unsigned char *p_local_part = *local_part;
    unsigned char *p_domain = *domain;

    ptr = addressLine;
    ptrLimit = addressLine + MAXEMAILNAMESIZE;

    /*    Validation step:    Handle [CFWS]    */
    if (MSG_IS_WSP(ptr[0])) {
        if (MSG_IS_CRLF(++ptr)) {
            ptr +=2;

            if (MSG_IS_WSP(ptr[0])) {
                ptr++;
            } else {
                return(FALSE);
            }
        }
    }

    /*    Validation step:    Handle comment    */
    if (ptr[0] == MSGADDR_COMMENT_START) {
        if (MsgIsComment(ptr, ptrLimit, &ptr)) {
            ;
        } else {
            return(FALSE);
        }
    }

#ifndef PROCESS_ADL
    if (ptr[0] == '@') {
        /* this is an adl address.  right now we want to skip this, but we don't just want to drop the code in case we need it later.
        rfc 2821 specifically states that we should accept but ignore the adl formatted address. */
        return (FALSE);
    }
#else
    /* Address could posibly have an A-d-l defined in RFC2821 */
    if (ptr[0] != '@') {
        ;
    } else {
        ptr++;

    HandleADL:     /* See rfc2821 4.1.2 */
        if (MSG_IS_ATEXT(ptr[0])) {
            /*    Specification: dot-atom-text
		  Validation step: Handle dot-atom-text    */
            while (++ptr < ptrLimit) {
                if (MSG_IS_ATEXT(ptr[0])) {
                    /*    Specification: 1*atext    */
                    continue;
                } else if (ptr[0] == '.') {
                    /*    Specification:    ("."1*atext)    */
                    continue;
                } else if (ptr[0] == ':') {
                    ptr++;
                    break;
                } else if ((ptr[0] == ',') && (ptr[1] == '@')) {
                    ptr += 2;
                    goto HandleADL;
                }

                return(FALSE);
            }
        } else if (ptr[0] == '[') {
            ptr++;

            /*    Validation step:    Handle dtext
		  Handle quoted-pair    */
            while (ptr < ptrLimit) {
                if (MSG_IS_DTEXT(ptr[0])) {
                    ptr++;
                    continue;
                } else if ((ptr[0] == '\\') && (MSG_IS_TEXT(ptr[1]))) {
                    ptr += 2;
                    continue;
                } else if (ptr[0] == ']') {
                    if ((ptr[1] == ',') && (ptr[2] == '@')) {
                        ptr += 3;
                        goto HandleADL;
                    } else if (ptr[1] == ':') {
                        ptr += 2;
                        break;
                    }
                    ptr++;
                    break;
                }
                return(FALSE);
            }
        } else {
            return(FALSE);
        }
    }
#endif /* process adl */

    /*    Validation step: Handle 1*atext*("."1*atext)
	  Validation step: Handle DQUOTE    */
    if (MSG_IS_ATEXT(ptr[0])) {
        /*    Specification: dot-atom-text
	      Validation step: Handle dot-atom-text    */
	if (local_part) {
	   *p_local_part = *ptr;
	   p_local_part++;
	}
        while (++ptr < ptrLimit) {
            if (MSG_IS_ATEXT(ptr[0])) {
                /*    Specification: 1*atext    */
                if (local_part) {
                    *p_local_part = *ptr;
                    p_local_part++;
                }
                continue;
            } else if (ptr[0] == '.') {
                /*    Specification:    ("."1*atext)    */
                if (local_part) {
                    *p_local_part = *ptr;
                    p_local_part++;
                }
                continue;
            } else if ((ptr[0] == ';') && (XplStrNCaseCmp(addressLine, "rfc822;", 7) == 0) && ((ptr - addressLine) == 6)) {
                /*    RFC 1891 - Additional parameters for RCPT and MAIL commands

		orcpt-parameter = "ORCPT=" original-recipient-address

		original-recipient-address = rfc822 ";" xtext */
                ptr++;

                if (MsgIsXText(ptr, ptrLimit, &ptr)) {
                    return(TRUE);
                }
                return(FALSE);
            }

            break;
        }
    } else if (ptr[0] == '"') {
        /*    Specification: *([FWS]qcontent)[FWS]DQUOTE
	      Validation step: Handle [FWS]    */
        if (MSG_IS_WSP(ptr[0])) {
            if (MSG_IS_CRLF(++ptr)) {
                ptr +=2;

                if (MSG_IS_WSP(ptr[0])) {
                    ptr++;
                } else {
                    return(FALSE);
                }
            }
        }

        /*    Validation step: Handle qtext
	      Handle quoted-pair
	      Handle [FWS]
	      Handle DQUOTE    */
        while (++ptr < ptrLimit) {
            if (MSG_IS_QTEXT(ptr[0])) {
                continue;
            } else if ((ptr[0] == '\\') && (MSG_IS_TEXT(ptr[1]))) {
                ptr++;
                continue;
            } else if (MSG_IS_WSP(ptr[0])) {
                if (MSG_IS_CRLF(++ptr)) {
                    ptr +=2;

                    if (MSG_IS_WSP(ptr[0])) {
                        continue;
                    }

                    return(FALSE);
                }
            } else if (ptr[0] == '"') {
                ptr++;
                break;
            }

            return(FALSE);
        }
    } else {
        return(FALSE);
    }

    if (ptr < ptrLimit) {
        /*    Specification: [CFWS]
	      Validation step: Handle [CFWS]
	      Handle [FWS]    */
        if (MSG_IS_WSP(ptr[0])) {
            if (MSG_IS_CRLF(++ptr)) {
                ptr +=2;

                if (MSG_IS_WSP(ptr[0])) {
                    ptr++;
                } else {
                    return(FALSE);
                }
            }
        }

        /*    Validation step:    Handle comment    */
        if (ptr[0] == MSGADDR_COMMENT_START) {
            if (MsgIsComment(ptr, ptrLimit, &ptr)) {
                ;
            } else {
                return(FALSE);
            }
        }

        if (ptr[0] == '@') {
            /*    RFC 2822 - 3.4.1 Addr-spec specification
		  addr-spec = local-part"@"domain
		  domain = dot-atom / domain-literal / obs-domain

		  domain-literal = [CFWS]"["*([FWS]dcontent)[FWS]"]"[CFWS]
		  dot-atom = [CFWS]dot-atom-text[CFWS]

		  dcontent = dtext / quoted-pair
		  dot-atom-text = 1*atext*("."1*atext)

		  FWS = ([*WSP CRLF] 1*WSP) / obs-FWS
		  CFWS = *([FWS] comment) (([FWS] comment) / FWS)

		  Validation step:    Handle [CFWS]    */
            ptr++;

            /*    Specification: [CFWS]
		  Validation step: Handle [CFWS]
		  Handle [FWS]    */
            if (MSG_IS_WSP(ptr[0])) {
                if (MSG_IS_CRLF(++ptr)) {
                    ptr +=2;

                    if (MSG_IS_WSP(ptr[0])) {
                        ptr++;
                    } else {
                        return(FALSE);
                    }
                }
            }
            
            /*    Validation step:    Handle comment    */
            if (ptr[0] == MSGADDR_COMMENT_START) {
                if (MsgIsComment(ptr, ptrLimit, &ptr)) {
                    ;
                } else {
                    return(FALSE);
                }
            }

            if (MSG_IS_ATEXT(ptr[0])) {
                /*    Specification: dot-atom-text
		      Validation step: Handle dot-atom-text    */
		if (domain) {
		  *p_domain = *ptr;
		  p_domain++;
		}
		
                while (++ptr < ptrLimit) {
                    if (MSG_IS_ATEXT(ptr[0])) {
                        /*    Specification: 1*atext    */
                        if (domain) {
                            *p_domain = *ptr;
                            p_domain++;
                        }
                        continue;
                    } else if (ptr[0] == '.') {
                        /*    Specification:    ("."1*atext)    */
                        if (domain) {
                            *p_domain = *ptr;
                            p_domain++;
                        }
                        continue;
                    }

                    break;
                }
            } else if (ptr[0] == '[') {
                ptr++;

                /*    Specification: *([FWS]dcontent)[FWS]"]"
		      Validation step: Handle [FWS]    */
                if (MSG_IS_WSP(ptr[0])) {
                    if (MSG_IS_CRLF(++ptr)) {
                        ptr +=2;

                        if (MSG_IS_WSP(ptr[0])) {
                            ptr++;
                        } else {
                            return(FALSE);
                        }
                    }
                }

                /*    Validation step:    Handle dtext
		      Handle quoted-pair    */
                while (ptr < ptrLimit) {
                    if (MSG_IS_DTEXT(ptr[0])) {
                        if (domain) {
                            *p_domain = *ptr;
                            p_domain++;
                        }

                        ptr++;
                        continue;
                    } else if ((ptr[0] == '\\') && (MSG_IS_TEXT(ptr[1]))) {
                        ptr += 2;
                        continue;
                    } else if (ptr[0] == ']') {
                        ptr++;
                        break;
                    } else if (MSG_IS_WSP(ptr[0])) {
                        /*    Validation step: Handle [FWS]    */
                        if (MSG_IS_CRLF(++ptr)) {
                            ptr +=2;

                            if (MSG_IS_WSP(ptr[0])) {
                                continue;
                            }
                        }
                    }

                    return(FALSE);
                }
            } else {
                return(FALSE);
            }

            /*    Specification: [CFWS]
		  Validation step: Handle [CFWS]
		  Handle [FWS]    */
            if (MSG_IS_WSP(ptr[0])) {
                if (MSG_IS_CRLF(++ptr)) {
                    ptr +=2;

                    if (MSG_IS_WSP(ptr[0])) {
                        ptr++;
                    } else {
                        return(FALSE);
                    }
                }
            }
            
            /*    Validation step:    Handle comment    */
            if (ptr[0] == MSGADDR_COMMENT_START) {
                if (MsgIsComment(ptr, ptrLimit, &ptr)) {
                    ;
                } else {
                    return(FALSE);
                }
            }
        }
        
        if ((strchr(delimiters, ptr[0]) != NULL) || (ptr[0] == '\0')) {
            return(TRUE);
        }
    }
    return(FALSE);
}
#endif
