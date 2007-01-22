#include <config.h>
#include <xpl.h>
#include <memmgr.h>
#include <bongoutil.h>

static void
TestUtf8String(char *utf8TestString)
{
    long len;
    char *utf7Buffer;
    char *utf8Buffer;
    long utf7BufferLen;
    long utf8BufferLen;

    /* TEST 1:  UTF-8 to UTF-7 */
    len = strlen(utf8TestString);
    

    utf7Buffer = MemMalloc(4 *len);
    fail_unless((long)utf7Buffer, "Malloc Failure");

    utf7BufferLen = Utf8ToModifiedUtf7(utf8TestString, strlen(utf8TestString), utf7Buffer, len * 4);

    if (utf7BufferLen < 0 ) {
        XplConsolePrintf("FolderUTF8toUTF7 returned %d\n", (int)utf7BufferLen);
        MemFree(utf7Buffer);
        fail_unless(0);
    }

    utf7Buffer[utf7BufferLen] = '\0';    

    /* UTF-7 back to UTF-8 */
    utf8Buffer = MemMalloc(len + 1);
    if (!utf8Buffer) {
        XplConsolePrintf("TestUtf8String failed to allocate %d bytes\n", (int)(len + 1));
        MemFree(utf7Buffer);
        fail_unless(0);
    }

    utf8BufferLen = ModifiedUtf7ToUtf8(utf7Buffer, utf7BufferLen, utf8Buffer, len + 1);
    if (utf8BufferLen < 0) {
        XplConsolePrintf("FolderUTF7toUTF8 returned %d\n", (int)utf8BufferLen);
        MemFree(utf7Buffer);
        MemFree(utf8Buffer);
        fail_unless(0);
    }

    utf8Buffer[utf8BufferLen] = '\0';

    /* compare original to result */
    if (strcmp(utf8TestString, utf8Buffer) != 0) {
        XplConsolePrintf("\nFAILED: Comparing original to  result\n");
        XplConsolePrintf("\n  original: %s\n  result:   %s\n", utf8TestString, utf8Buffer);
        MemFree(utf7Buffer);
        MemFree(utf8Buffer);
        fail_unless(0);
    }

    MemFree(utf7Buffer);
    MemFree(utf8Buffer);
}


START_TEST(utf8_to_modified_utf7_back_to_utf8)
{
    char buffer[1000];
    MemoryManagerOpen("BongoKeyword_Test");

    TestUtf8String("/this/folder/has/hierarchy");
    TestUtf8String("Me & Stan/Me&Stan");
    TestUtf8String("Trailing&");
    TestUtf8String("Escape&-Middle");

    /* something in Chinese */
    buffer[0]  = (char)0xE5;
    buffer[1]  = (char)0x95;
    buffer[2]  = (char)0x86;
    buffer[3]  = (char)0xE6;
    buffer[4]  = (char)0xA5;
    buffer[5]  = (char)0xAD;
    buffer[6]  = (char)0xE8;
    buffer[7] = (char)0x88;
    buffer[8] = (char)0x87;
    buffer[9] = (char)0xE7;
    buffer[10] = (char)0xB6;
    buffer[11] = (char)0x93;
    buffer[12] = (char)0xE6;
    buffer[13] = (char)0xBF;
    buffer[14] = (char)0x9F;
    buffer[15] = (char)0x00;

    TestUtf8String(buffer);

    /* Junk/< same Chinese>/ */
    buffer[0]  = (char)0x4A;
    buffer[1]  = (char)0x75;
    buffer[2]  = (char)0x6E;
    buffer[3]  = (char)0x6B;
    buffer[4]  = (char)0x2F;
    buffer[5]  = (char)0xE5;
    buffer[6]  = (char)0x95;
    buffer[7]  = (char)0x86;
    buffer[8]  = (char)0xE6;
    buffer[9]  = (char)0xA5;
    buffer[10]  = (char)0xAD;
    buffer[11]  = (char)0xE8;
    buffer[12] = (char)0x88;
    buffer[13] = (char)0x87;
    buffer[14] = (char)0xE7;
    buffer[15] = (char)0xB6;
    buffer[16] = (char)0x93;
    buffer[17] = (char)0xE6;
    buffer[18] = (char)0xBF;
    buffer[19] = (char)0x9F;
    buffer[20] = (char)0x00;

    TestUtf8String(buffer);

    buffer[0]  = (char)0x48;
    buffer[1]  = (char)0x69;
    buffer[2]  = (char)0x20;
    buffer[3]  = (char)0x4D;
    buffer[4]  = (char)0x6F;
    buffer[5]  = (char)0x6D;
    buffer[6]  = (char)0x20;
    buffer[7] = (char)0x2D;
    buffer[8] = (char)0xE2;
    buffer[9] = (char)0x98;
    buffer[10] = (char)0xBA;
    buffer[11] = (char)0x2D;
    buffer[12] = (char)0x21;
    buffer[13] = (char)0x00;

    TestUtf8String(buffer);

    /* something in Chinese */
    buffer[0]  = (char)0xE5;
    buffer[1]  = (char)0x95;
    buffer[2]  = (char)0x86;
    buffer[3]  = (char)0xE6;
    buffer[4]  = (char)0xA5;
    buffer[5]  = (char)0xAD;
    buffer[6]  = (char)0xE8;
    buffer[7] = (char)0x88;
    buffer[8] = (char)0x87;
    buffer[9] = (char)0xE7;
    buffer[10] = (char)0xB6;
    buffer[11] = (char)0x93;
    buffer[12] = (char)0xE6;
    buffer[13] = (char)0xBF;
    buffer[14] = (char)0x9F;
    buffer[15] = (char)0x07;
    buffer[16] = (char)0x00;

    TestUtf8String(buffer);

    /* something in Chinese */
    buffer[0]  = (char)0xE5;
    buffer[1]  = (char)0x95;
    buffer[2]  = (char)0x86;
    buffer[3]  = (char)0xE6;
    buffer[4]  = (char)0xA5;
    buffer[5]  = (char)0xAD;
    buffer[6]  = (char)0xE8;
    buffer[7] = (char)0x88;
    buffer[8] = (char)0x87;
    buffer[9] = (char)0xE7;
    buffer[10] = (char)0xB6;
    buffer[11] = (char)0x93;
    buffer[12] = (char)0xE6;
    buffer[13] = (char)0xBF;
    buffer[14] = (char)0x9F;
    buffer[15] = (char)0x07;
    buffer[16] = (char)0x09;
    buffer[17] = (char)0x00;

    TestUtf8String(buffer);

    /* something in Chinese */
    buffer[0]  = (char)0xE5;
    buffer[1]  = (char)0x95;
    buffer[2]  = (char)0x86;
    buffer[3]  = (char)0xE6;
    buffer[4]  = (char)0xA5;
    buffer[5]  = (char)0xAD;
    buffer[6]  = (char)0xE8;
    buffer[7] = (char)0x88;
    buffer[8] = (char)0x87;
    buffer[9] = (char)0xE7;
    buffer[10] = (char)0xB6;
    buffer[11] = (char)0x93;
    buffer[12] = (char)0xE6;
    buffer[13] = (char)0xBF;
    buffer[14] = (char)0x9F;
    buffer[15] = (char)0x07;
    buffer[16] = (char)0x09;
    buffer[17] = (char)0x07;
    buffer[18] = (char)0x00;

    TestUtf8String(buffer);

    /* something in Chinese */
    buffer[0]  = (char)0xE5;
    buffer[1]  = (char)0x95;
    buffer[2]  = (char)0x86;
    buffer[3]  = (char)0xE6;
    buffer[4]  = (char)0xA5;
    buffer[5]  = (char)0xAD;
    buffer[6]  = (char)0xE8;
    buffer[7] = (char)0x88;
    buffer[8] = (char)0x87;
    buffer[9] = (char)0xE7;
    buffer[10] = (char)0xB6;
    buffer[11] = (char)0x93;
    buffer[12] = (char)0xE6;
    buffer[13] = (char)0xBF;
    buffer[14] = (char)0x9F;
    buffer[15] = (char)0x07;
    buffer[16] = (char)0x09;
    buffer[17] = (char)0x07;
    buffer[18] = (char)0x09;
    buffer[19] = (char)0x00;

    TestUtf8String(buffer);

    /* something in Chinese */
    buffer[0]  = (char)0xE5;
    buffer[1]  = (char)0x95;
    buffer[2]  = (char)0x86;
    buffer[3]  = (char)0xE6;
    buffer[4]  = (char)0xA5;
    buffer[5]  = (char)0xAD;
    buffer[6]  = (char)0xE8;
    buffer[7] = (char)0x88;
    buffer[8] = (char)0x87;
    buffer[9] = (char)0xE7;
    buffer[10] = (char)0xB6;
    buffer[11] = (char)0x93;
    buffer[12] = (char)0xE6;
    buffer[13] = (char)0xBF;
    buffer[14] = (char)0x9F;
    buffer[15] = (char)0x07;
    buffer[16] = (char)0x09;
    buffer[17] = (char)0x07;
    buffer[18] = (char)0x09;
    buffer[19] = (char)0x07;
    buffer[20] = (char)0x00;

    TestUtf8String(buffer);

    /* something in Chinese */
    buffer[0]  = (char)0xE5;
    buffer[1]  = (char)0x95;
    buffer[2]  = (char)0x86;
    buffer[3]  = (char)0xE6;
    buffer[4]  = (char)0xA5;
    buffer[5]  = (char)0xAD;
    buffer[6]  = (char)0xE8;
    buffer[7] = (char)0x88;
    buffer[8] = (char)0x87;
    buffer[9] = (char)0xE7;
    buffer[10] = (char)0xB6;
    buffer[11] = (char)0x93;
    buffer[12] = (char)0xE6;
    buffer[13] = (char)0xBF;
    buffer[14] = (char)0x9F;
    buffer[15] = (char)0x07;
    buffer[16] = (char)0x09;
    buffer[17] = (char)0x07;
    buffer[18] = (char)0x09;
    buffer[19] = (char)0x07;
    buffer[20] = (char)0x09;
    buffer[21] = (char)0x00;

    TestUtf8String(buffer);

    /* something in Chinese */
    buffer[0]  = (char)0xE5;
    buffer[1]  = (char)0x95;
    buffer[2]  = (char)0x86;
    buffer[3]  = (char)0xE6;
    buffer[4]  = (char)0xA5;
    buffer[5]  = (char)0xAD;
    buffer[6]  = (char)0xE8;
    buffer[7] = (char)0x88;
    buffer[8] = (char)0x87;
    buffer[9] = (char)0xE7;
    buffer[10] = (char)0xB6;
    buffer[11] = (char)0x93;
    buffer[12] = (char)0xE6;
    buffer[13] = (char)0xBF;
    buffer[14] = (char)0x9F;
    buffer[15] = (char)0x07;
    buffer[16] = (char)0x09;
    buffer[17] = (char)0x07;
    buffer[18] = (char)0x09;
    buffer[19] = (char)0x07;
    buffer[20] = (char)0x09;
    buffer[21] = (char)0x07;
    buffer[22] = (char)0x00;

    TestUtf8String(buffer);


    /* something in Chinese */
    buffer[0]  = (char)0xE5;
    buffer[1]  = (char)0x95;
    buffer[2]  = (char)0x86;
    buffer[3]  = (char)0xE6;
    buffer[4]  = (char)0xA5;
    buffer[5]  = (char)0xAD;
    buffer[6]  = (char)0xE8;
    buffer[7] = (char)0x88;
    buffer[8] = (char)0x87;
    buffer[9] = (char)0xE7;
    buffer[10] = (char)0xB6;
    buffer[11] = (char)0x93;
    buffer[12] = (char)0xE6;
    buffer[13] = (char)0xBF;
    buffer[14] = (char)0x9F;
    buffer[15]  = (char)0x2F;
    buffer[16]  = (char)0x4A;
    buffer[17]  = (char)0x75;
    buffer[18]  = (char)0x6E;
    buffer[19]  = (char)0x6B;
    buffer[20]  = (char)0x00;

    TestUtf8String(buffer);


    /* Supplementary Character */
    buffer[0]  = (char)0xF0;
    buffer[1]  = (char)0x92;
    buffer[2]  = (char)0x97;
    buffer[3]  = (char)0xBF;
    buffer[4]  = (char)0x00;

    TestUtf8String(buffer);




    MemoryManagerClose("BongoKeyword_Test");
}
END_TEST

void static
TestUtf8ToUtf7(char *in, char *answer)
{
    long len;
    char out[1000];

    len = Utf8ToModifiedUtf7(in, 4, out, sizeof(out));
    if (len < 0) {
        XplConsolePrintf("FolderUTF8toUTF7 returned %d\n", (int)len);
        fail_unless(0);
    }
    
    out[len] = '\0';
    if (strcmp(out, answer) != 0) {
        XplConsolePrintf("\n\n\n%s \n and\n%s are not equal\n\n\n", out, answer);
        fail_unless(0);
    }
}


START_TEST(utf8_to_modified_utf7)
{
    char in[1000];

    MemoryManagerOpen("BongoKeyword_Test");

    /* Supplementary Character */
    in[0]  = (char)0xF0;
    in[1]  = (char)0x92;
    in[2]  = (char)0x97;
    in[3]  = (char)0xBF;
    in[4]  = (char)0x00;
    
    TestUtf8ToUtf7(in, "&2And,w-");


    MemoryManagerClose("BongoKeyword_Test");
}
END_TEST
