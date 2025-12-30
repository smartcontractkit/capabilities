// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.0;

contract MessageEmitter {
    event MessageEmitted(string message);

    // New event with multiple indexed parameters for multi-topic log trigger tests
    event MultiTopicEmitted(
        uint256 indexed topic2,
        uint256 indexed topic3,
        uint256 indexed topic4,
        string message
    );

    function emitMessage(string calldata message) external {
        emit MessageEmitted(message);
    }

    // Emits the MultiTopicEmitted event so tests can exercise topic2-4 filtering
    function emitMultiTopic(
        uint256 topic2,
        uint256 topic3,
        uint256 topic4,
        string calldata message
    ) external {
        emit MultiTopicEmitted( topic2, topic3, topic4, message);
    }
}
