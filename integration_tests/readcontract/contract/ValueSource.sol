// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.0;

contract ValueSource {
    function GetValue() public pure returns (int[] memory) {
        int[] memory values = new int[](3);
        values[0] = 21;
        values[1] = 42;
        values[2] = 63;
        return values;
    }
}
