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

#include <config.h>
#include <xpl.h>
#include <memmgr.h>
#include <bongoutil.h>

#include <gnutls/openssl.h>

const unsigned char *Base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=";

BOOL
QuickNCmp(unsigned char *str1, unsigned char *str2, int len)
{
    while (--len && *str1 && *str2 && toupper(*str1) == toupper(*str2)) {
        str1++;
        str2++;
    }

    return(toupper(*str1) == toupper(*str2));
}

BOOL
QuickCmp(unsigned char *str1, unsigned char *str2)
{
    while (*str1 && *str2 && toupper(*str1) == toupper(*str2)) {
        str1++;
        str2++;
    }

    return(toupper(*str1) == toupper(*str2));
}

const unsigned char HexChars[] = {
/*  0x00    000    NUL    Null                      */  0,
/*  0x01    001    SOH    Start of Heading          */  0,
/*	0x02    002    STX    Start Text                */  0,
/*  0x03    003    ETX    End Text                  */  0,
/*  0x04    004    EOT    End of Transmisison       */  0,
/*  0x05    005    ENQ    Enquiry                   */  0,
/*  0x06    006    ACK    Acknowledge               */  0,
/*  0x07    007    BEL    Bell                      */  0,
/*  0x08    008    BS     Backspace                 */  0,
/*  0x09    009    HT     Horizontal Tab            */  0,
/*  0x0A    010    LF     Linefeed                  */  0,
/*  0x0B    011    VT     Vertical Tab              */  0,
/*  0x0C    012    FF     Formfeed                  */  0,
/*  0x0D    013    CR     Carriage Return           */  0,
/*  0x0E    014    SO     Shift Out                 */  0,
/*  0x0F    015    SI     Shift In                  */  0,
/*  0x10    016    DLE    Data Link Escape          */  0,
/*  0x11    017    DC1    Device Control 1          */  0,
/*  0x12    018    DC2    Device Control 2          */  0,
/*  0x13    019    DC3    Device Control 3          */  0,
/*  0x14    020    DC4    Device Control 4          */  0,
/*  0x15    021    NAK    Negative Acknowledgement	*/  0,
/*  0x16    022    SYN    Synchronous Idle          */  0,
/*  0x17    023    ETB    End Transmission Block    */  0,
/*  0x18    024    CAN    Cancel                    */  0,
/*  0x19    025    EM     End Medium                */  0,
/*  0x1A    026    EOF    End of File               */  0,
/*  0x1B    027    ESC    Escape                    */  0,
/*  0x1C    028    FS     File Separator            */  0,
/*  0x1D    029    GS     Group Separator           */  0,
/*  0x1E    030    HOM    Home                      */  0,
/*  0x1F    031    NEW    Newline                   */  0,
/*  0x20    032    SPA    Space                     */  0,
/*  0x21    033    !      Exclamation Point         */  0,
/*  0x22    034    "      Double Quote              */  0,
/*  0x23    035    #      Pound Sign                */  0,
/*  0x24    036    $      Dollar Sign               */  0,
/*  0x25    037    %      Percent                   */  0,
/*  0x26    038    &      Ampersand                 */  0,
/*  0x27    039    '      Apostrophe                */  0,
/*  0x28    040    (      Right Parenthesis         */  0,
/*  0x29    041    )      Left Parenthesis          */  0,
/*  0x2A    042    *      Asterick                  */  0,
/*  0x2B    043    +      Plus                      */  0,
/*  0x2C    044    ,      Comma                     */  0,
/*  0x2D    045    -      Minus                     */  0,
/*  0x2E    046    .      Period                    */  0,
/*  0x2F    047    /      Forward Slash             */  0,
/*  0x30    048    0      Zero                      */  1,
/*  0x31    049    1      One                       */  2,
/*  0x32    050    2      Two                       */  3,
/*  0x33    051    3      Three                     */  4,
/*  0x34    052    4      Four                      */  5,
/*  0x35    053    5      Five                      */  6,
/*  0x36    054    6      Six                       */  7,
/*  0x37    055    7      Seven                     */  8,
/*  0x38    056    8      Eight                     */  9,
/*  0x39    057    9      Nine                      */  10,
/*  0x3A    058    :      Colon                     */  0,
/*  0x3B    059    ;      Semi-Colon                */  0,
/*  0x3C    060    <      Less-than                 */  0,
/*  0x3D    061    =      Equal                     */  0,
/*  0x3E    062    >      Greater-than              */  0,
/*  0x3F    063    ?      Question Mark             */  0,
/*  0x40    064    @      At                        */  0,
/*  0x41    065    A      Uppercase A               */  11,
/*  0x42    066    B      Uppercase B               */  12,
/*  0x43    067    C      Uppercase C               */  13,
/*  0x44    068    D      Uppercase D               */  14,
/*  0x45    069    E      Uppercase E               */  15,
/*  0x46    070    F      Uppercase F               */  16,
/*  0x47    071    G      Uppercase G               */  0,
/*  0x48    072    H      Uppercase H               */  0,
/*  0x49    073    I      Uppercase I               */  0,
/*  0x4A    074    J      Uppercase J               */  0,
/*  0x4B    075    K      Uppercase K               */  0,
/*  0x4C    076    L      Uppercase L               */  0,
/*  0x4D    077    M      Uppercase M               */  0,
/*  0x4E    078    N      Uppercase N               */  0,
/*  0x4F    079    O      Uppercase O               */  0,
/*  0x50    080    P      Uppercase P               */  0,
/*  0x51    081    Q      Uppercase Q               */  0,
/*  0x52    082    R      Uppercase R               */  0,
/*  0x53    083    S      Uppercase S               */  0,
/*  0x54    084    T      Uppercase T               */  0,
/*  0x55    085    U      Uppercase U               */  0,
/*  0x56    086    V      Uppercase V               */  0,
/*  0x57    087    W      Uppercase W               */  0,
/*  0x58    088    X      Uppercase X               */  0,
/*  0x59    089    Y      Uppercase Y               */  0,
/*  0x5A    090    Z      Uppercase Z               */  0,
/*  0x5B    091    [      Left Square Bracket       */  0,
/*  0x5C    092    \      Back Slash                */  0,
/*  0x5D    093    ]      Right Square Bracket      */  0,
/*  0x5E    094    ^      Carrot                    */  0,
/*  0x5F    095    _      Underscore                */  0,
/*  0x60    096    `      Tick                      */  0,
/*  0x61    097    a      Lowercase a               */  11,
/*  0x62    098    b      Lowercase b               */  12,
/*  0x63    099    c      Lowercase c               */  13,
/*  0x64    100    d      Lowercase d               */  14,
/*  0x65    101    e      Lowercase e               */  15,
/*  0x66    102    f      Lowercase f               */  16,
/*  0x67    103    g      Lowercase g               */  0,
/*  0x68    104    h      Lowercase h               */  0,
/*  0x69    105    i      Lowercase i               */  0,
/*  0x6A    106    j      Lowercase j               */  0,
/*  0x6B    107    k      Lowercase k               */  0,
/*  0x6C    108    l      Lowercase l               */  0,
/*  0x6D    109    m      Lowercase m               */  0,
/*  0x6E    110    n      Lowercase n               */  0,
/*  0x6F    111    o      Lowercase o               */  0,
/*  0x70    112    p      Lowercase p               */  0,
/*  0x71    113    q      Lowercase q               */  0,
/*  0x72    114    r      Lowercase r               */  0,
/*  0x73    115    s      Lowercase s               */  0,
/*  0x74    116    t      Lowercase t               */  0,
/*  0x75    117    u      Lowercase u               */  0,
/*  0x76    118    v      Lowercase v               */  0,
/*  0x77    119    w      Lowercase w               */  0,
/*  0x78    120    x      Lowercase x               */  0,
/*  0x79    121    y      Lowercase y               */  0,
/*  0x7A    122    z      Lowercase z               */  0,
/*  0x7B    123    {      Left Curly Brace          */  0,
/*  0x7C    124    |      Pipe                      */  0,
/*  0x7D    125    }      Right Curly Brace         */  0,
/*  0x7E    126    ~      Tilde                     */  0,
/*  0x7F    127    DEL    Delete                    */  0,
/*  0x80    128                                     */  0,
/*  0x81    129                                     */  0,
/*  0x82    130                                     */  0,
/*  0x83    131                                     */  0,
/*  0x84    132                                     */  0, 
/*  0x85    133                                     */  0, 
/*  0x86    134                                     */  0, 
/*  0x87    135                                     */  0, 
/*  0x88    136                                     */  0, 
/*  0x89    137                                     */  0, 
/*  0x8A    138                                     */  0, 
/*  0x8B    139                                     */  0, 
/*  0x8C    140                                     */  0, 
/*  0x8D    141                                     */  0, 
/*  0x8E    142                                     */  0, 
/*  0x8F    143                                     */  0, 
/*  0x90    144                                     */  0, 
/*  0x91    145                                     */  0, 
/*  0x92    146                                     */  0, 
/*  0x93    147                                     */  0, 
/*  0x94    148                                     */  0, 
/*  0x95    149                                     */  0, 
/*  0x96    150                                     */  0, 
/*  0x97    151                                     */  0, 
/*  0x98    152                                     */  0, 
/*  0x99    153                                     */  0, 
/*  0x9A    154                                     */  0, 
/*  0x9B    155                                     */  0, 
/*  0x9C    156                                     */  0, 
/*  0x9D    157                                     */  0, 
/*  0x9E    158                                     */  0, 
/*  0x9F    159                                     */  0, 
/*  0xA0    160                                     */  0, 
/*  0xA1    161                                     */  0, 
/*  0xA2    162                                     */  0, 
/*  0xA3    163                                     */  0, 
/*  0xA4    164                                     */  0, 
/*  0xA5    165                                     */  0, 
/*  0xA6    166                                     */  0, 
/*  0xA7    167                                     */  0, 
/*  0xA8    168                                     */  0, 
/*  0xA9    169                                     */  0, 
/*  0xAA    170                                     */  0, 
/*  0xAB    171                                     */  0, 
/*  0xAC    172                                     */  0, 
/*  0xAD    173                                     */  0, 
/*  0xAE    174                                     */  0, 
/*  0xAF    175                                     */  0, 
/*  0xB0    176                                     */  0, 
/*  0xB1    177                                     */  0, 
/*  0xB2    178                                     */  0, 
/*  0xB3    179                                     */  0, 
/*  0xB4    180                                     */  0, 
/*  0xB5    181                                     */  0, 
/*  0xB6    182                                     */  0, 
/*  0xB7    183                                     */  0, 
/*  0xB8    184                                     */  0, 
/*  0xB9    185                                     */  0, 
/*  0xBA    186                                     */  0, 
/*  0xBB    187                                     */  0, 
/*  0xBC    188                                     */  0, 
/*  0xBD    189                                     */  0, 
/*  0xBE    190                                     */  0, 
/*  0xBF    191                                     */  0, 
/*  0xC0    192                                     */  0, 
/*  0xC1    193                                     */  0, 
/*  0xC2    194                                     */  0, 
/*  0xC3    195                                     */  0, 
/*  0xC4    196                                     */  0, 
/*  0xC5    197                                     */  0, 
/*  0xC6    198                                     */  0, 
/*  0xC7    199                                     */  0, 
/*  0xC8    200                                     */  0, 
/*  0xC9    201                                     */  0, 
/*  0xCA    202                                     */  0, 
/*  0xCB    203                                     */  0, 
/*  0xCC    204                                     */  0, 
/*  0xCD    205                                     */  0, 
/*  0xCE    206                                     */  0, 
/*  0xCF    207                                     */  0, 
/*  0xD0    208                                     */  0, 
/*  0xD1    209                                     */  0, 
/*  0xD2    210                                     */  0, 
/*  0xD3    211                                     */  0, 
/*  0xD4    212                                     */  0, 
/*  0xD5    213                                     */  0, 
/*  0xD6    214                                     */  0, 
/*  0xD7    215                                     */  0, 
/*  0xD8    216                                     */  0, 
/*  0xD9    217                                     */  0, 
/*  0xDA    218                                     */  0, 
/*  0xDB    219                                     */  0, 
/*  0xDC    220                                     */  0, 
/*  0xDD    221                                     */  0, 
/*  0xDE    222                                     */  0, 
/*  0xDF    223                                     */  0, 
/*  0xE0    224                                     */  0, 
/*  0xE1    225                                     */  0, 
/*  0xE2    226                                     */  0, 
/*  0xE3    227                                     */  0, 
/*  0xE4    228                                     */  0, 
/*  0xE5    229                                     */  0, 
/*  0xE6    230                                     */  0, 
/*  0xE7    231                                     */  0, 
/*  0xE8    232                                     */  0, 
/*  0xE9    233                                     */  0, 
/*  0xEA    234                                     */  0, 
/*  0xEB    235                                     */  0, 
/*  0xEC    236                                     */  0, 
/*  0xED    237                                     */  0, 
/*  0xEE    238                                     */  0, 
/*  0xEF    239                                     */  0, 
/*  0xF0    240                                     */  0, 
/*  0xF1    241                                     */  0, 
/*  0xF2    242                                     */  0, 
/*  0xF3    243                                     */  0, 
/*  0xF4    244                                     */  0, 
/*  0xF5    245                                     */  0, 
/*  0xF6    246                                     */  0, 
/*  0xF7    247                                     */  0, 
/*  0xF8    248                                     */  0, 
/*  0xF9    249                                     */  0, 
/*  0xFA    250                                     */  0, 
/*  0xFB    251                                     */  0, 
/*  0xFC    252                                     */  0, 
/*  0xFD    253                                     */  0, 
/*  0xFE    254                                     */  0, 
/*  0xFF    255                                     */  0
};


uint64_t
HexToUInt64(char *hexString, char **endp)
{
    long i;
    long len;
    uint64_t value;
    uint64_t base;
    char *ptr;

    ptr = hexString;

    do {
        if (HexChars[(unsigned char)(*ptr)]) {
            ptr++;
            continue;
        }
        break;
    } while(TRUE);

    len = ptr - hexString;   
 
    if (endp) {
        *endp = ptr;
    }

    value = 0;
    base = 1;

    for (i = len; i > 0; i--) {
        ptr--;
        value += ((HexChars[(unsigned char)(*ptr)] - 1) * base);
        base *= 16;
    }
    return(value);
}

static void
ProtocolCommandTreeRotateLeft(ProtocolCommandTree *tree, ProtocolCommand *command)
{
    register ProtocolCommand *x = command;
    register ProtocolCommand *y;
                                                                                                                                                                            
    y = x->right;
                                                                                                                                                                            
    x->right = y->left;
                                                                                                                                                                            
    if (y->left != tree->sentinel) {
        y->left->parent = x;
    }
                                                                                                                                                                            
    y->parent = x->parent;
                                                                                                                                                                            
    if (x->parent != tree->sentinel) {
        if (x == x->parent->left) {
            x->parent->left = y;
        } else {
            x->parent->right = y;
        }
    } else {
        tree->root = y;
    }
                                                                                                                                                                            
    y->left = x;
                                                                                                                                                                            
    x->parent = y;
                                                                                                                                                                            
    return;
}
                                                                                                                                                                            
static void
ProtocolCommandTreeRotateRight(ProtocolCommandTree *tree, ProtocolCommand *command)
{
    ProtocolCommand *x = command;
    ProtocolCommand *y;
                                                                                                                                                                            
    y = x->left;
                                                                                                                                                                            
    x->left = y->right;
                                                                                                                                                                            
    if (y->right != tree->sentinel) {
        y->right->parent = x;
    }
                                                                                                                                                                            
    y->parent = x->parent;
                                                                                                                                                                            
    if (x->parent != tree->sentinel) {
        if (x == x->parent->right) {
            x->parent->right = y;
        } else {
            x->parent->left = y;
        }
    } else {
        tree->root = y;
    }
                                                                                                                                                                            
    y->right = x;
                                                                                                                                                                            
    x->parent = y;
                                                                                                                                                                            
    return;
}
                                                                                                                                                                            
ProtocolCommand *
ProtocolCommandTreeSearch(ProtocolCommandTree *tree, const unsigned char *command)
{
    const unsigned char *k1;
    const unsigned char *k2;
    ProtocolCommand *x;
    ProtocolCommand *y;
                                                                                                                                                                            
    if (tree && command) {
        y = tree->sentinel;
        x = tree->root;
                                                                                                                                                                            
        k1 = x->name;
        k2 = command;
        while (x != tree->sentinel) {
            y = x;
                                                                                                                                                                            
            if (*k1 < toupper(*k2)) {
                x = x->left;
                                                                                                                                                                            
                k1 = x->name;
                k2 = command;

                continue;
            }

            if (*k1 > toupper(*k2)) {
                x = x->right;
                                                                                                                                                                            
                k1 = x->name;
                k2 = command;

                continue;
            }

            if (*(++k1)) {
                k2++;
                continue;
            }

            if (!*(++k2) || (*k2 == ' ')) {
                return(x);
            }
        }
    }
                                                                                                                                                                            
    return(NULL);
}
                                                                                                                                                                            
static void
ProtocolCommandTreeInsert(ProtocolCommandTree *tree, ProtocolCommand *command)
{
    const unsigned char *k1;
    const unsigned char *k2;
    BOOL l = FALSE;
    ProtocolCommand *x;
    ProtocolCommand *y;
                                                                                                                                                                            
    if (tree && command) {
        y = tree->sentinel;
        x = tree->root;
                                                                                                                                                                            
        k1 = x->name;
        k2 = command->name;
        while (x != tree->sentinel) {
            y = x;
                                                                                                                                                                            
            if (*k1 < *k2) {
                l = TRUE;
                                                                                                                                                                            
                x = x->left;
                                                                                                                                                                            
                k1 = x->name;
                k2 = command->name;

                continue;
            }

            if (*k1 > *k2) {
                l = FALSE;
                                                                                                                                                                            
                x = x->right;
                                                                                                                                                                            
                k1 = x->name;
                k2 = command->name;

                continue;
            }

            if (*k1) {
                k1++;
                k2++;

                continue;
            }

            return;
        }
                                                                                                                                                                            
        XplSafeIncrement(tree->nodes);
                                                                                                                                                                            
        x = command;
                                                                                                                                                                            
        x->parent = y;
                                                                                                                                                                            
        if (y != tree->sentinel) {
            if (l == TRUE) {
                y->left = x;
            } else {
                y->right = x;
            }
        } else {
            tree->root = x;
        }
                                                                                                                                                                            
        x->left = tree->sentinel;
        x->right = tree->sentinel;
                                                                                                                                                                            
        x->color = RedCommand;
                                                                                                                                                                            
        while ((x != tree->root) && (x->parent->color == RedCommand)) {
            if (x->parent == x->parent->parent->left) {
                y = x->parent->parent->right;
                if (y->color == RedCommand) {
                    x->parent->color = BlackCommand;
                                                                                                                                                                            
                    y->color = BlackCommand;
                                                                                                                                                                            
                    x->parent->parent->color = RedCommand;
                                                                                                                                                                            
                    x = x->parent->parent;
                } else {
                    if (x == x->parent->right) {
                        x = x->parent;
                                                                                                                                                                            
                        ProtocolCommandTreeRotateLeft(tree, x);
                    }
                                                                                                                                                                            
                    x->parent->color = BlackCommand;
                                                                                                                                                                            
                    x->parent->parent->color = RedCommand;
                                                                                                                                                                            
                    ProtocolCommandTreeRotateRight(tree, x->parent->parent);
                }
            } else {
                y = x->parent->parent->left;
                if (y->color == RedCommand) {
                    x->parent->color = BlackCommand;
                                                                                                                                                                            
                    y->color = BlackCommand;
                                                                                                                                                                            
                    x->parent->parent->color = RedCommand;
                                                                                                                                                                            
                    x = x->parent->parent;
                } else {
                    if (x == x->parent->left) {
                        x = x->parent;
                                                                                                                                                                            
                        ProtocolCommandTreeRotateRight(tree, x);
                    }
                                                                                                                                                                            
                    x->parent->color = BlackCommand;
                                                                                                                                                                            
                    x->parent->parent->color = RedCommand;
                                                                                                                                                                            
                    ProtocolCommandTreeRotateLeft(tree, x->parent->parent);
                }
            }
        }
                                                                                                                                                                            
        tree->root->color = BlackCommand;
    }
                                                                                                                                                                            
    return;
}
                                                                                                                                                                            
void
LoadProtocolCommandTree(ProtocolCommandTree *tree, ProtocolCommand *commands)
{
    ProtocolCommand *cmd;
                                                                                                                                                                            
    if (tree) {
        cmd = &(tree->proxy);
                                                                                                                                                                            
        cmd->color = BlackCommand;
                                                                                                                                                                            
        cmd->parent = NULL;
                                                                                                                                                                            
        cmd->left = NULL;
        cmd->right = NULL;
                                                                                                                                                                            
        cmd->name = NULL;
        cmd->help = NULL;
                                                                                                                                                                            
        cmd->length = 0;
                                                                                                                                                                            
        cmd->handler = NULL;
                                                                                                                                                                            
        tree->sentinel = cmd;
        tree->root = cmd;
                                                                                                                                                                            
        XplSafeWrite(tree->nodes, 0);
                                                                                                                                                                            
        cmd = commands;
                                                                                                                                                                            
        while (cmd->name) {
            ProtocolCommandTreeInsert(tree, cmd);
                                                                                                                                                                            
            cmd++;
        }
    }
                                                                                                                                                                            
                                                                                                                                                                            
    return;
}
                                                                                                                                                                            
unsigned char *
DecodeBase64(unsigned char *EncodedString)
{
    int i;
    int j;
    int length = strlen(EncodedString);
    unsigned char *buffer = MemStrdup(EncodedString);
    unsigned char *ptr = buffer;
    unsigned char table[257];
    unsigned char *ptr2;

    memset(table, 127, 256);
    table[256] = '\0';

    for (i = 0; i < 65; i++) {
        table[Base64Chars[i]] = i;
    }

    for (i=0; i<length; i++) {
        if ((*ptr = table[(unsigned char)buffer[i]]) <= 64) {
            ptr++;
        }
    }

    length = ((int)(ptr - buffer) & ~3);

    ptr = ptr2 = buffer;

    for (i = 0; i < length; i += 4) {
        if (ptr[3] == 64) {
            if (ptr[2] == 64) {
                j  = *ptr++ << 2;
                j += *ptr++ >> 4;

                *ptr2++ = (unsigned char)(j & 255);
    
                break;
            }

            j  = (*ptr++ << 10);
            j += (*ptr++ <<  4);
            j += (*ptr++ >>  2);

            *ptr2++ = (unsigned char)(j >> 8);
            *ptr2++ = (unsigned char)(j &  255);

            break;
        }

        j  = (*ptr++ << 18);
        j += (*ptr++ << 12);
        j += (*ptr++ <<  6);
        j += (*ptr++);

        *ptr2++ = (unsigned char)((j>>16) & 255);
        *ptr2++ = (unsigned char)((j>>8)  & 255);
        *ptr2++ = (unsigned char)( j      & 255);
    }

    strncpy(EncodedString, buffer, (int)(ptr2 - buffer));

    EncodedString[(int)(ptr2 - buffer)]='\0';

    MemFree(buffer);

    return(EncodedString);
}

unsigned char *
EncodeBase64(const unsigned char *UnencodedString)
{
    int length;
    int lengthOut;
    int groups;
    int i;
    int lines;
    int width = 0;
    int na;
    int nb;
    int nc;
    int remaining;
    unsigned char *buffer;
    unsigned char *out;
    unsigned char cha;
    unsigned char chb;
    unsigned char chc;
    unsigned char chd;
    unsigned char *ptr;
    const unsigned char *in;

    if (UnencodedString) {
        length = strlen(UnencodedString);
        if (!length) {
            return(MemStrdup("\r\n"));
        }
    } else {
        return(NULL);
    }
    
    lengthOut = ((length+2)/3)*4;
    groups = length / 3;
    lines = (lengthOut + 75) / 76;

    lengthOut += 2 * lines;

    buffer = MemMalloc(lengthOut);
    out = buffer;
    in = UnencodedString;

    for (i = 0; i < groups; i++)
    {
        na = (unsigned char)(*in++);
        nb = (unsigned char)(*in++);
        nc = (unsigned char)(*in++);

        cha = (unsigned char)(na >> 2);
        chb = (unsigned char)(((na << 4) + (nb >> 4)) & 63);
        chc = (unsigned char)(((nb << 2) + (nc >> 6)) & 63);
        chd = (unsigned char)(nc & 63);

        *out++ = Base64Chars[cha];
        *out++ = Base64Chars[chb];
        *out++ = Base64Chars[chc];
        *out++ = Base64Chars[chd];

        /* insert a CRLF every 76 characters */
        if (((width += 4) == 76) && lines) {
            *out++ = '\r';
            *out++ = '\n';

            width = 0;
            lines--;
        }
    }

    remaining = length - groups * 3;

    if (remaining == 1) {
        na = (unsigned char)(*in++);

        *out++ = Base64Chars[na >> 2];
        *out++ = Base64Chars[(na & 3) << 4];
        *out++ = '=';
        *out++ = '=';
    } else if (remaining == 2) {
        na = (unsigned char)(*in++);
        nb = (unsigned char)(*in++);

        *out++ = Base64Chars[na>>2];
        *out++ = Base64Chars[((na&3)<<4)+(nb>>4)];
        *out++ = Base64Chars[((nb&15)<<2)];
        *out++ = '=';
    }


    if (lines) {
        *out++ = '\r';
        *out++ = '\n';
    }

    ptr = MemMalloc(lengthOut + 1);
    if (ptr) {
        strncpy(ptr, buffer, lengthOut);
        ptr[lengthOut] = '\0';
    }

    MemFree(buffer);

    return(ptr);
}

BOOL 
HashCredential(unsigned char *Credential, unsigned char *Hash)
{
    unsigned char *delim;
    unsigned char *srcPtr;
    unsigned char *dstPtr;
    unsigned char *dstEnd;
    unsigned char digest[NMAP_CRED_DIGEST_SIZE];
    unsigned long i;
    unsigned long len;
    xpl_hash_context ctx;

    if (Credential) {
        len = strlen(Credential);
        if (len >= NMAP_CRED_STORE_SIZE) {
            srcPtr = Credential;
            dstPtr = Hash;
            dstEnd = Hash + NMAP_HASH_SIZE;
            
            XplHashNew(&ctx, XPLHASH_MD5);
            XplHashWrite(&ctx, srcPtr, NMAP_CRED_CHUNK_SIZE);
            XplHashFinalBytes(&ctx, digest, XPLHASH_MD5BYTES_LENGTH);

            srcPtr += NMAP_CRED_CHUNK_SIZE;

            for (i = 0; i < NMAP_CRED_DIGEST_SIZE; i++) {
                if (dstPtr < dstEnd) {
                    *dstPtr = digest[i];
                    dstPtr++;
                }
            }

             do {
                XplHashNew(&ctx, XPLHASH_MD5);
                XplHashWrite(&ctx, srcPtr, NMAP_CRED_CHUNK_SIZE);
                XplHashWrite(&ctx, Hash, dstPtr - Hash);
                XplHashFinalBytes(&ctx, digest, XPLHASH_MD5BYTES_LENGTH);

                srcPtr += NMAP_CRED_CHUNK_SIZE;

                for (i = 0; i < NMAP_CRED_DIGEST_SIZE; i++) {
                    if (dstPtr < dstEnd) {
                        *dstPtr = digest[i];
                        dstPtr++;
                    }
                }
            } while (dstPtr < dstEnd);

            /* Hash now contains a non-terminated 128 byte octet string */

            return(TRUE);
        }
    }

    return(FALSE);
}

int
BongoStringSplit(char *str, char sep, char **ret, int retsize)
{
    int num;
    char *cur;
    char *ptr;
    
    cur = str;
    num = 0;
    while (*cur && num < retsize - 1) {
        ptr = cur;
        ptr = strchr(cur, sep);

        if (ptr != cur) {
            ret[num++] = cur;
            if (!ptr) {
                return num;
            }
        }
        
        while (*ptr == sep) {
            *ptr++ = '\0';
        }
        cur = ptr;
    }

    /* the last member always includes the rest of the string */
    if (*cur) {
        ret[num++] = cur;
    }

    return num;
}

int 
BongoMemStringSplit(const char *str, char sep, char **ret, int retsize)
{
    int num;
    const char *cur;
    const char *ptr;
    
    cur = str;
    num = 0;
    while (*cur && num < retsize - 1) {
        ptr = cur;
        ptr = strchr(cur, sep);

        if (ptr != cur) {
            ret[num++] = MemStrndup(cur, ptr - cur);
            if (!ptr) {
                return num;
            }
        }
        
        while (*ptr == sep) {
            ptr++;
        }
        cur = ptr;
    }

    /* the last member always includes the rest of the string */
    if (*cur) {
        ret[num++] = MemStrdup(cur);
    }

    return num;    
}

void 
BongoMemStringSplitFree(char **ret, int num)
{
    int i;
    
    for (i = 0; i < num; i++) {
        MemFree(ret[i]);
    }
}

char *
BongoStringJoin(char **strings, int num, char sep)
{
    int i;
    int size;
    char *ret;

    size = 0;
    for (i = 0; i < num; i++) {
        size += strlen(strings[i]);
        size += 1; /* Separator */
    }

    ret = MemMalloc(size + 1);
    
    char *p = ret;
    for (i = 0; i < num; i++) {
        int len = strlen(strings[i]);
        strcpy(p, strings[i]);
        p += len;
        if (i < (num - 1)) {
            *p++ = sep;
        }
    }
    *p++ = '\0';

    return ret;
}


char *
BongoStrTrim(char *str)
{
    char *start;
    int len;
    
    start = str;
    while (*start && isspace(*start)) {
        start++;
    }

    len = strlen(start);
    
    while (len && isspace(start[len - 1])) {
        start[len - 1] = '\0';
        len--;
    }

    memmove(str, start, len + 1);
    
    return str;
}

char *
BongoStrTrimDup(const char *str)
{
    const char *start;
    int len;
    
    start = str;
    while (*start && isspace(*start)) {
        start++;
    }

    len = strlen(start);

    while (len && isspace(start[len - 1])) {
        len--;
    }

    return MemStrndup(start, len);
}

void
BongoStrToLower(char *str)
{
    char *p;
    for (p = str; p && *p; p++) {
        *p = tolower(*p);
    }
}

char *
BongoMemStrToLower(const char *str)
{
    char *newStr = MemStrdup(str);
    BongoStrToLower(newStr);
    return newStr;
}

void
BongoStrToUpper(char *str)
{
    char *p;
    for (p = str; p && *p; p++) {
        *p = toupper(*p);
    }
}

char *
BongoMemStrToUpper(const char *str)
{
    char *newStr = MemStrdup(str);
    BongoStrToUpper(newStr);
    return newStr;
}

/* Cut a string separated by whitespace, keeping quoted strings together */

char *
Collapse(char *ch) {
    char end;
    if (*ch == '"') {
        end ='"';
        ch++;
    } else {
        end = ' ';
    }
        
    while (*ch && *ch != end) {
        if (*ch == '\\') {
            memmove(ch, ch + 1, strlen (ch));
        }
        ch++;
    }

    if (*ch) {
        return ch;
    } else {
        return NULL;
    }
}

int
CommandSplit(char *str, char **ret, int retsize)
{
    int num;
    char *cur;
    char *ptr;

    cur = str;
    num = 0;
    while (*cur && num < retsize - 1) {
        ptr = cur;

        ptr = Collapse(cur);
        if (ptr != cur) {
            if (*cur == '"') {
                cur++;
            }
            
            ret[num++] = cur;
            
            if (!ptr) {
                return num;
            }

            *ptr++ = '\0';        
        }

        while (*ptr == ' ') {
            *ptr++ = '\0';
        }
        cur = ptr;
    }

    /* the last member always includes the rest of the string */
    if (*cur) {
        ret[num++] = cur;
    }

    return num;
}

char *
BongoStrCaseStr(char *haystack, char *needle)
{
    char *ptr = haystack;
    char first = toupper(*needle);
    unsigned long len = strlen(needle);

    while (*ptr) {
        if ((toupper(*ptr) != first) || (XplStrNCaseCmp(ptr, needle, len) != 0)) {
            ptr++;
            continue;
        }
        return(ptr);
    }
    return(NULL);
}
