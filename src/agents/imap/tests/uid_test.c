#include "../uid.c"

START_TEST(uid_range_to_sequence_range)
{
    long ccode;
    MessageInformation message[20];
    unsigned long start;
    unsigned long end;

    message[0].uid = 20;
    message[1].uid = 22;

    ccode = TestUidToSequenceRange(message, 2, 100, 120, &start, &end);
    fail_unless(ccode == STATUS_UID_NOT_FOUND);

    ccode = TestUidToSequenceRange(message, 2, 1, 12, &start, &end);
    fail_unless(ccode == STATUS_UID_NOT_FOUND);

    ccode = TestUidToSequenceRange(message, 2, 19, 12, &start, &end);
    fail_unless(ccode == STATUS_UID_NOT_FOUND);

    ccode = TestUidToSequenceRange(message, 2, 22, 120, &start, &end);
    fail_unless(ccode == STATUS_CONTINUE);
    fail_unless(start == 1);
    fail_unless(end == 1);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 1));

    ccode = TestUidToSequenceRange(message, 2, 21, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 1));

    ccode = TestUidToSequenceRange(message, 2, 20, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 1));

    ccode = TestUidToSequenceRange(message, 2, 20, 21, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 0));

    ccode = TestUidToSequenceRange(message, 2, 20, 22, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 1));

    ccode = TestUidToSequenceRange(message, 2, 20, 19, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 0));

    ccode = TestUidToSequenceRange(message, 2, 2, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 1));



    message[0].uid = 5;
    message[1].uid = 6;
    message[2].uid = 7;

    ccode = TestUidToSequenceRange(message, 3, 100, 120, &start, &end);
    fail_unless(ccode == STATUS_UID_NOT_FOUND);

    ccode = TestUidToSequenceRange(message, 3, 8, 120, &start, &end);
    fail_unless(ccode == STATUS_UID_NOT_FOUND);

    ccode = TestUidToSequenceRange(message, 3, 1, 3, &start, &end);
    fail_unless(ccode == STATUS_UID_NOT_FOUND);

    ccode = TestUidToSequenceRange(message, 3, 4, 2, &start, &end);
    fail_unless(ccode == STATUS_UID_NOT_FOUND);

    ccode = TestUidToSequenceRange(message, 3, 7, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 2) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 6, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 5, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 4, 21, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 2, 5, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 0));

    ccode = TestUidToSequenceRange(message, 3, 2, 6, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 1));

    ccode = TestUidToSequenceRange(message, 3, 2, 7, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 4, 8, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 5, 8, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 5, 7, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 6, 7, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 5, 6, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 1));

    ccode = TestUidToSequenceRange(message, 3, 6, 7, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 7, 7, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 2) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 6, 6, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 1));

    ccode = TestUidToSequenceRange(message, 3, 5, 5, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 0));

    message[0].uid = 5;
    message[1].uid = 6;
    message[2].uid = 7;
    message[3].uid = 8;

    ccode = TestUidToSequenceRange(message, 4, 100, 120, &start, &end);
    fail_unless(ccode == STATUS_UID_NOT_FOUND);

    ccode = TestUidToSequenceRange(message, 4, 9, 120, &start, &end);
    fail_unless(ccode == STATUS_UID_NOT_FOUND);

    ccode = TestUidToSequenceRange(message, 4, 1, 3, &start, &end);
    fail_unless(ccode == STATUS_UID_NOT_FOUND);

    ccode = TestUidToSequenceRange(message, 4, 4, 2, &start, &end);
    fail_unless(ccode == STATUS_UID_NOT_FOUND);

    ccode = TestUidToSequenceRange(message, 4, 8, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 3) && (end == 3));

    ccode = TestUidToSequenceRange(message, 4, 7, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 2) && (end == 3));

    ccode = TestUidToSequenceRange(message, 4, 6, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 3));

    ccode = TestUidToSequenceRange(message, 4, 5, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 3));

    ccode = TestUidToSequenceRange(message, 4, 4, 21, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 3));

    ccode = TestUidToSequenceRange(message, 4, 2, 5, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 0));

    ccode = TestUidToSequenceRange(message, 4, 2, 6, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 1));

    ccode = TestUidToSequenceRange(message, 4, 2, 7, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 2));

    ccode = TestUidToSequenceRange(message, 4, 2, 8, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 3));

    ccode = TestUidToSequenceRange(message, 4, 2, 9, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 3));

    ccode = TestUidToSequenceRange(message, 4, 4, 9, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 3));

    ccode = TestUidToSequenceRange(message, 4, 5, 9, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 3));

    ccode = TestUidToSequenceRange(message, 4, 5, 8, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 3));

    ccode = TestUidToSequenceRange(message, 4, 5, 7, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 2));

    ccode = TestUidToSequenceRange(message, 4, 6, 7, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 2));

    ccode = TestUidToSequenceRange(message, 4, 5, 6, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 1));

    ccode = TestUidToSequenceRange(message, 4, 6, 7, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 2));

    ccode = TestUidToSequenceRange(message, 4, 7, 7, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 2) && (end == 2));

    ccode = TestUidToSequenceRange(message, 4, 6, 6, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 1));

    ccode = TestUidToSequenceRange(message, 4, 5, 5, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 0));

    message[0].uid = 50;
    message[1].uid = 61;
    message[2].uid = 70;

    ccode = TestUidToSequenceRange(message, 3, 100, 120, &start, &end);
    fail_unless(ccode == STATUS_UID_NOT_FOUND);

    ccode = TestUidToSequenceRange(message, 3, 71, 120, &start, &end);
    fail_unless(ccode == STATUS_UID_NOT_FOUND);

    ccode = TestUidToSequenceRange(message, 3, 1, 3, &start, &end);
    fail_unless(ccode == STATUS_UID_NOT_FOUND);

    ccode = TestUidToSequenceRange(message, 3, 49, 2, &start, &end);
    fail_unless(ccode == STATUS_UID_NOT_FOUND);

    ccode = TestUidToSequenceRange(message, 3, 70, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 2) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 69, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 2) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 68, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 2) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 62, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 2) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 61, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 60, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 59, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 52, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 51, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 50, 120, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 49, 80, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 2, 50, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 0));

    ccode = TestUidToSequenceRange(message, 3, 2, 51, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 0));

    ccode = TestUidToSequenceRange(message, 3, 2, 52, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 0));

    ccode = TestUidToSequenceRange(message, 3, 2, 59, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 0));

    ccode = TestUidToSequenceRange(message, 3, 2, 60, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 0));

    ccode = TestUidToSequenceRange(message, 3, 2, 61, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 1));

    ccode = TestUidToSequenceRange(message, 3, 2, 62, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 1));

    ccode = TestUidToSequenceRange(message, 3, 2, 65, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 1));

    ccode = TestUidToSequenceRange(message, 3, 2, 69, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 1));

    ccode = TestUidToSequenceRange(message, 3, 2, 70, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 49, 71, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 50, 70, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 51, 70, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 60, 70, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 5, 62, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 1));

    ccode = TestUidToSequenceRange(message, 3, 59, 70, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 65, 71, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 2) && (end == 2));

    ccode = TestUidToSequenceRange(message, 3, 60, 65, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 1) && (end == 1));

    ccode = TestUidToSequenceRange(message, 3, 50, 59, &start, &end);
    fail_unless((ccode == STATUS_CONTINUE) && (start == 0) && (end == 0));


}
END_TEST

